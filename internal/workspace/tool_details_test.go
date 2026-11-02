package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

func toolDetailChat(text string) chatmodel.Session {
	return chatmodel.Session{ID: "detail-chat", Turns: []chatmodel.Turn{{ID: "turn", PartStart: 0, InputEnd: 1, PartEnd: 4}},
		Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "question"},
			{Role: "assistant", Kind: "tool_use", Name: "Read", Text: `{"file_path":"src/a.go"}`},
			{Role: "tool", Kind: "tool_result", Text: text},
			{Role: "assistant", Kind: "text", Text: "answer"},
		}}}
}

func TestToolSummaryMatchesActivityLabelsAndStatistics(t *testing.T) {
	for _, tc := range []struct{ name, text, kind, path, query string }{
		{"Read", `{"file_path":"src/a.go"}`, "read", "src/a.go", ""},
		{"functions.exec_command", `{"cmd":"rg TODO","path":"src","pattern":"TODO"}`, "search", "src", "TODO"},
		{"apply_patch", "*** Begin Patch\n*** Add File: a/b/c/d.go\n+find cat\n*** End Patch", "create", "\u2026/b/c/d.go", ""},
		{"apply_patch", "*** Begin Patch\n*** Delete File: gone.go\n*** End Patch", "delete", "gone.go", ""},
		{"Edit", `{"file_path":"ui.tsx","new_string":"find cat"}`, "write", "ui.tsx", ""},
		{"WebSearch", `{"query":"Go cache"}`, "web", "", "Go cache"},
		{"functions.exec_command", `{"cmd":"ls src"}`, "list", "", ""},
		{"reasoning", strings.Repeat("thinking ", 10000), "thinking", "", ""},
	} {
		part := chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: tc.name, Text: tc.text}
		summary := summarizeTool(part)
		if summary.Kind != tc.kind || summary.Path != tc.path || summary.Query != tc.query || summary.Version != toolBodyVersion(tc.text) || summary.Bytes != len(tc.text) || len(summary.Preview) > 2048 {
			t.Fatalf("%s: invalid summary %+v", tc.name, summary)
		}
	}
	part := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n+more\n"}
	summary := summarizeTool(part)
	if summary.Changes == nil || summary.Changes.Additions != 2 || summary.Changes.Deletions != 1 {
		t.Fatal("patch statistics changed")
	}
	part.Text = "Changed files +12 -3"
	if summarizeTool(part).Stats != "+12 -3" {
		t.Fatal("inline statistics missing")
	}
}

func TestActivityPreviewDoesNotCopyFullToolBody(t *testing.T) {
	for _, text := range []string{"small", strings.Repeat("x", 500000), strings.Repeat(" ", 500000) + "tail", strings.Repeat("\u4e2d", 500000), "\n\t "} {
		for _, name := range []string{"Read", "  exec", strings.Repeat("name", 100)} {
			want := chatmodel.Clip(strings.TrimSpace(name+": "+text), 240)
			if got := activityPreview(name, text); got != want {
				t.Fatalf("legacy activity preview changed: name bytes=%d text bytes=%d got=%q want=%q", len(name), len(text), got, want)
			}
		}
	}
}

func TestToolChangeSuffixMatchesExistingStatsPattern(t *testing.T) {
	pattern := regexp.MustCompile(`(?:^|\s)(\+\d+)\s+(-\d+)\s*$`)
	for _, value := range []string{"", "output", "+12 -3", "Changed +12 -3\n", "before+12 -3", "before +12-3", "+123\n\t-234\n", " + -3", "+12 -", "+12 -3 text", strings.Repeat("plain output ", 100000) + " +12 -3"} {
		want := ""
		if match := pattern.FindStringSubmatch(value); match != nil {
			want = match[1] + " " + match[2]
		}
		if got := toolChangeSuffix(value); got != want {
			t.Fatal("tool change suffix semantics changed")
		}
	}
}

func TestToolDetailProjectionIsSmallAndDoesNotMutateSource(t *testing.T) {
	full := toolDetailChat(strings.Repeat("large-tool-output\n", 200000))
	slim := slimToolDetails(full)
	encoded, err := json.Marshal(slim)
	if err != nil || len(encoded) > 2500 || strings.Contains(string(encoded), "large-tool-output") {
		t.Fatal("tool body leaked into browsing payload", len(encoded), err)
	}
	if slim.Transcript.Parts[0].Text != "question" || slim.Transcript.Parts[3].Text != "answer" || full.Transcript.Parts[2].Text == "" || full.Transcript.Parts[2].Tool != nil {
		t.Fatal("projection changed conversation content/source")
	}
	oldRef := slim.Transcript.Parts[2].Tool.Ref
	full.Transcript.Parts[2].Text += "appended"
	updated := slimToolDetails(full)
	if oldRef != updated.Transcript.Parts[2].Tool.Ref || slim.Transcript.Parts[2].Tool.Version == updated.Transcript.Parts[2].Tool.Version {
		t.Fatal("tool identity/version did not distinguish append")
	}
	other := full
	other.ID = "other-chat"
	if slimToolDetails(other).Transcript.Parts[2].Tool.Ref == oldRef {
		t.Fatal("tool references not scoped to chat")
	}
}

func TestToolDetailCursorHandlesAppendReplaceUnicodeAndEOF(t *testing.T) {
	full := toolDetailChat(strings.Repeat("\u4e2d\u6587\U0001f642", 20000))
	ref := slimToolDetails(full).Transcript.Parts[2].Tool.Ref
	first, ok := readToolDetailPage(full, ref, 0, "")
	if !ok || first.Reset || first.Next > toolDetailPageBytes || !utf8.ValidString(first.Text) {
		t.Fatal("invalid first page")
	}
	var out strings.Builder
	out.WriteString(first.Text)
	page := first
	for page.Next < page.Total {
		page, ok = readToolDetailPage(full, ref, page.Next, page.Prefix)
		if !ok || page.Reset || !utf8.ValidString(page.Text) || page.Offset != out.Len() {
			t.Fatal("lost/repeated page")
		}
		out.WriteString(page.Text)
	}
	if out.String() != full.Transcript.Parts[2].Text {
		t.Fatal("full detail was truncated")
	}
	eof, ok := readToolDetailPage(full, ref, page.Next, page.Prefix)
	if !ok || eof.Reset || eof.Text != "" {
		t.Fatal("EOF cursor invalid")
	}
	full.Transcript.Parts[2].Text += "tail"
	appended, ok := readToolDetailPage(full, ref, page.Next, page.Prefix)
	if !ok || appended.Reset || appended.Text != "tail" {
		t.Fatal("append resent an existing prefix")
	}
	full.Transcript.Parts[2].Text = "replacement"
	replaced, ok := readToolDetailPage(full, ref, page.Next, page.Prefix)
	if !ok || !replaced.Reset || replaced.Offset != 0 || replaced.Text != "replacement" {
		t.Fatal("replacement was appended to old body")
	}
	full.Transcript.Parts[2].Text = "\u4e2d\u6587"
	invalid, _ := readToolDetailPage(full, ref, 1, toolBodyVersion(full.Transcript.Parts[2].Text[:1]))
	if !invalid.Reset || !utf8.ValidString(invalid.Text) {
		t.Fatal("accepted a cursor inside a codepoint")
	}
	if _, ok := readToolDetailPage(full, ref, -1, ""); ok {
		t.Fatal("accepted negative cursor")
	}
	if _, ok := readToolDetailPage(full, "missing", 0, ""); ok {
		t.Fatal("accepted missing tool")
	}
}

func TestNativeHistoryCacheSeparatesToolBodiesAndReusesSummaries(t *testing.T) {
	store := testStore(t)
	_, reads := countNativeHistoryReads(store)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 2)
	chat := chatmodel.Session{ID: "cache-details", Backend: chatmodel.BackendCodex, NativeSessionPath: path}
	full, err := store.readHistory(chat)
	if err != nil {
		t.Fatal(err)
	}
	store.historyCache.mu.Lock()
	for _, file := range store.historyCache.files {
		if len(file.details) != 1 {
			t.Fatal("duplicate tool bodies were not shared")
		}
		for _, transcript := range file.parts {
			for _, part := range transcript.Parts {
				if toolPart(part) && (part.Text != "" || part.Tool == nil || part.Tool.SourceText != "") {
					t.Fatal("cached summary still retains a full body")
				}
			}
		}
	}
	store.historyCache.mu.Unlock()
	if full.Transcript.Parts[1].Text != strings.Repeat("x", 8000) {
		t.Fatal("internal snapshot lost original detail")
	}
	full.Transcript.Parts[1].Tool.Path = "poison"
	full.Transcript.Parts[1].Text = "changed"
	again, err := store.readHistory(chat)
	if err != nil || reads.Load() != 1 || again.Transcript.Parts[1].Tool.Path == "poison" || again.Transcript.Parts[1].Text != strings.Repeat("x", 8000) {
		t.Fatal("cache isolation/reuse failed", err)
	}
	assertNoTurnTables(t, store)
}

func TestToolDetailHTTPChecksAccessAndLazyHistoryContract(t *testing.T) {
	store := testStore(t)
	owner := testUser(t, store, "detail-owner")
	testUser(t, store, "detail-outsider")
	project := testProject(t, store, owner)
	chat, _ := store.CreateChat(owner.ID, project.ID, chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 3, 18)
	_ = store.SetNativeSessionPath(chat.ID, "detail-native", path)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	cookie, _ := loginRequest(t, app, owner.Username)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/chats/"+chat.ID+path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	w := get("?tool_details=lazy")
	var slim chatmodel.Session
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &slim) != nil {
		t.Fatal("lazy session failed", w.Code)
	}
	if strings.Contains(w.Body.String(), strings.Repeat("x", 100)) || slim.Transcript.Parts[1].Text != "" || slim.Transcript.Parts[1].Tool.Ref == "" {
		t.Fatal("session included raw tool bodies")
	}
	ref := slim.Transcript.Parts[1].Tool.Ref
	w = get("/tool-detail?ref=" + ref)
	var detail toolDetailPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Text != strings.Repeat("x", 8000) {
		t.Fatal("detail not available on demand", w.Code)
	}
	if detail.ServerMillis <= 0 || !strings.HasPrefix(w.Header().Get("Server-Timing"), "tool-detail;dur=") {
		t.Fatal("detail read timing omitted")
	}
	w = get("/history?tool_details=lazy&before=" + url.QueryEscape(slim.History.Before))
	if w.Code != 200 || strings.Contains(w.Body.String(), strings.Repeat("x", 100)) {
		t.Fatal("older page bypassed lazy projection")
	}
	if get("/tool-detail?ref="+ref+"&offset=-1").Code != 400 || get("/tool-detail?ref=missing").Code != 404 {
		t.Fatal("invalid detail requests accepted")
	}
	cookie, _ = loginRequest(t, app, "detail-outsider")
	if get("/tool-detail?ref="+ref).Code != 404 {
		t.Fatal("tool bodies bypassed chat permissions")
	}
}

func TestLazyToolStreamSendsVersionsNotBodies(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "detail-stream")
	project := testProject(t, store, user)
	chat, _ := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	cookie, _ := loginRequest(t, app, user.Username)
	base, _ := store.Get(chat.ID)
	turn, _ := store.BeginTurn(chat.ID, "question")
	turn = app.seedLiveTurn(base, turn)
	appendTurnPart(&turn, chatmodel.Part{Role: "tool", Kind: "tool_result", Text: strings.Repeat("private-output", 100000)})
	app.liveMu.Lock()
	app.live[chat.ID] = turn
	app.liveMu.Unlock()
	_ = store.SaveRunningTurn(chat.ID, turn)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events?tool_details=lazy", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	previousVersion, previousRef := "", ""
	for {
		select {
		case chunk := <-stream.writes:
			frame := string(chunk)
			if !strings.HasPrefix(frame, "event: snapshot\n") && !strings.HasPrefix(frame, "event: turn\n") {
				continue
			}
			if strings.Contains(frame, "private-output") || len(frame) > 8000 {
				t.Fatal("stream resent tool body", len(frame))
			}
			_, data, _ := strings.Cut(frame, "data: ")
			var value chatmodel.Session
			if json.Unmarshal([]byte(data), &value) != nil {
				t.Fatal("invalid stream frame")
			}
			parts := value.Transcript.Parts
			summary := parts[len(parts)-1].Tool
			if summary == nil {
				t.Fatal("stream omitted tool summary")
			}
			if previousVersion != "" {
				if summary.Version != previousVersion {
					if summary.Ref != previousRef {
						t.Fatal("live append changed detail identity")
					}
					return
				}
				continue
			}
			previousVersion, previousRef = summary.Version, summary.Ref
			app.liveMu.Lock()
			updated := app.live[chat.ID]
			updated.Parts = append([]chatmodel.Part(nil), updated.Parts...)
			updated.Parts[len(updated.Parts)-1].Text += "tail"
			app.live[chat.ID] = updated
			app.liveMu.Unlock()
		case <-deadline.C:
			t.Fatal("tool version update was not streamed")
		}
	}
}
