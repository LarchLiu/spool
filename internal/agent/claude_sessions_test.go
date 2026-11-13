package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"
)

func writeClaudeListFixture(t *testing.T, home, project, name string, order int, records ...map[string]any) string {
	t.Helper()
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
	folder := claudeEncodedDirectory(project)
	if len(folder) > 200 {
		var hash int32
		for _, c := range utf16.Encode([]rune(project)) {
			hash = hash*31 + int32(c)
		}
		value := int64(hash)
		if value < 0 {
			value = -value
		}
		folder = folder[:200] + "-" + strconv.FormatInt(value, 36)
	}
	path := filepath.Join(home, "projects", folder, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := json.NewEncoder(file).Encode(record); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000+int64(order), 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeListUser(project, text string) map[string]any {
	return map[string]any{"type": "user", "parentUuid": nil, "cwd": project, "timestamp": "2026-01-02T03:04:05Z", "message": map[string]any{"role": "user", "content": text}, "gitBranch": "main", "entrypoint": "cli"}
}

func claudeListCorpus(t *testing.T) (home, project string) {
	t.Helper()
	home = t.TempDir()
	project, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	writeClaudeListFixture(t, home, project, "ordinary", 1, claudeListUser(project, "First request"))
	writeClaudeListFixture(t, home, project, "renamed", 2, claudeListUser(project, "Original prompt"),
		map[string]any{"type": "ai-title", "aiTitle": "Generated title"},
		map[string]any{"type": "custom-title", "customTitle": "Chosen title"},
		map[string]any{"type": "tag", "tag": "review"},
		map[string]any{"type": "summary", "summary": "Less important summary"})
	headless := claudeListUser(project, "Programmatic request")
	headless["entrypoint"] = "sdk-ts"
	writeClaudeListFixture(t, home, project, "headless", 3, headless)
	sidechain := claudeListUser(project, "Hidden child")
	sidechain["isSidechain"] = true
	writeClaudeListFixture(t, home, project, "sidechain", 20, sidechain)
	writeClaudeListFixture(t, home, project, "empty", 21, map[string]any{"type": "progress"})
	writeClaudeListFixture(t, home, project, "summary", 4, claudeListUser(project, "Prompt before summary"), map[string]any{"type": "summary", "summary": "Summary title"})
	image := claudeListUser(project, "")
	image["message"] = map[string]any{"role": "user", "content": []any{struct {
		Type   string `json:"type"`
		Source any    `json:"source"`
	}{"image", map[string]any{"data": strings.Repeat("x", 1400)}}}}
	writeClaudeListFixture(t, home, project, "image", 5, image)
	writeClaudeListFixture(t, home, project, "large-tail-title", 6, claudeListUser(project, "Large transcript"), map[string]any{"type": "progress", "data": strings.Repeat("x", 140000)}, map[string]any{"type": "custom-title", "customTitle": "Tail title"})
	return home, project
}

func claudeListEdgeCorpus(t *testing.T, home, project string) {
	t.Helper()
	writeClaudeListFixture(t, home, project, "slash-command", 31, claudeListUser(project, "<command-name>/help</command-name>"), claudeListUser(project, "Real question after command"))
	writeClaudeListFixture(t, home, project, "only-command", 32, claudeListUser(project, "<command-name>/compact</command-name>"))
	writeClaudeListFixture(t, home, project, "bash", 33, claudeListUser(project, "<bash-input>git status</bash-input>"))
	writeClaudeListFixture(t, home, project, "long-unicode", 34, claudeListUser(project, strings.Repeat("你好🙂", 80)))
	writeClaudeListFixture(t, home, project, "meta-before-user", 35, claudeListUser(project, "<system-reminder>Ignore this</system-reminder>"), claudeListUser(project, "Actual prompt"))
	writeClaudeListFixture(t, home, project, "last-prompt", 36, claudeListUser(project, "First prompt"), map[string]any{"type": "summary", "summary": "Summary", "lastPrompt": "Most recent prompt"})
	path := writeClaudeListFixture(t, home, project, "sidecar", 37, claudeListUser(project, "Before sidecar"))
	sidecar := strings.TrimSuffix(path, ".jsonl")
	if err := os.MkdirAll(sidecar, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "custom-title.json"), []byte(`{"customTitle":"Sidecar title"}`), 0600); err != nil {
		t.Fatal(err)
	}
	continued := uuid.NewSHA1(uuid.NameSpaceOID, []byte("continuation")).String()
	writeClaudeListFixture(t, home, project, "superseded", 38, claudeListUser(project, "Old prompt"), map[string]any{"type": "continued-in", "continuedInSessionId": continued})
	writeClaudeListFixture(t, home, project, "continuation", 39, claudeListUser(project, "Continued prompt"))
	daemon := claudeListUser(project, "Daemon prompt")
	daemon["sessionKind"] = "daemon-worker"
	writeClaudeListFixture(t, home, project, "daemon", 40, daemon)
	relocated := t.TempDir()
	writeClaudeListFixture(t, home, project, "relocated", 41, claudeListUser(project, "Relocated prompt"), map[string]any{"type": "relocated", "relocatedCwd": relocated})
}

func TestClaudeSessionListEdgeCases(t *testing.T) {
	home, project := claudeListCorpus(t)
	claudeListEdgeCorpus(t, home, project)
	sessions, err := (ClaudeSessionLister{}).ListSessions(context.Background(), ListSessionsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	for _, s := range sessions {
		titles[s.Title] = true
	}
	for _, title := range []string{"Real question after command", "/compact", "! git status", "Actual prompt", "Most recent prompt", "Sidecar title", "Continued prompt", "Relocated prompt"} {
		if !titles[title] {
			t.Fatalf("missing %q: %+v", title, titles)
		}
	}
	if titles["Old prompt"] {
		t.Fatal("superseded session remained visible")
	}
}

func TestClaudeSessionListCachesUnchangedTranscripts(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	path := writeClaudeListFixture(t, home, project, "cached", 1, claudeListUser(project, "First title"))
	source := NewClaudeSessionLister()
	list := func() LocalSession {
		t.Helper()
		sessions, err := source.ListSessions(context.Background(), ListSessionsOptions{})
		if err != nil || len(sessions) != 1 {
			t.Fatalf("cached list: %+v %v", sessions, err)
		}
		return sessions[0]
	}
	if first := list(); first.Title != "First title" {
		t.Fatalf("initial read: %+v", first)
	}
	// Same size and mtime: the transcript must not be read again.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), "First title", "Other title", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if cached := list(); cached.Title != "First title" {
		t.Fatalf("unchanged transcript was read again: %+v", cached)
	}
	later := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if changed := list(); changed.Title != "Other title" || changed.UpdatedAt != later.UnixMilli() {
		t.Fatalf("changed transcript was served from cache: %+v", changed)
	}
	sidecar := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "custom-title.json")
	if err := os.MkdirAll(filepath.Dir(sidecar), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte(`{"customTitle":"Sidecar rename"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if renamed := list(); renamed.Title != "Sidecar rename" {
		t.Fatalf("sidecar rename was hidden by the cache: %+v", renamed)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if sessions, err := source.ListSessions(context.Background(), ListSessionsOptions{}); err != nil || len(sessions) != 0 || len(source.cache.entries) != 0 {
		t.Fatalf("deleted transcript stayed cached: %+v %v", sessions, err)
	}
}

func TestClaudeSessionListWorkspaceMetadata(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	// A head sample without any cwd: the directory comes from the tail.
	lateCwd := writeClaudeListFixture(t, home, project, "late-cwd", 1,
		map[string]any{"type": "summary", "summary": "Compacted"},
		map[string]any{"type": "progress", "data": strings.Repeat("x", 140000)},
		claudeListUser(project, "Prompt after compaction"))
	// Fallback titles that complete history would replace are provisional.
	writeClaudeListFixture(t, home, project, "summary-and-prompt", 2, claudeListUser(project, "First prompt"),
		map[string]any{"type": "summary", "summary": "Summary"}, map[string]any{"type": "last-prompt", "lastPrompt": "Most recent prompt"})
	writeClaudeListFixture(t, home, project, "prompt-only", 3, claudeListUser(project, "Only prompt"))
	untitled := claudeListUser(project, "<system-reminder>context only</system-reminder>")
	writeClaudeListFixture(t, home, project, "untitled", 4, untitled)
	upper := writeClaudeListFixture(t, home, project, "upper-case", 5, claudeListUser(project, "Upper-case file name"))
	upperID := strings.ToUpper(strings.TrimSuffix(filepath.Base(upper), ".jsonl"))
	if err := os.Rename(upper, filepath.Join(filepath.Dir(upper), upperID+".jsonl")); err != nil {
		t.Fatal(err)
	}
	sessions, err := (ClaudeSessionLister{}).ListSessions(context.Background(), ListSessionsOptions{IncludeUntitled: true})
	if err != nil || len(sessions) != 5 {
		t.Fatalf("workspace listing: %+v %v", sessions, err)
	}
	byTitle := map[string]LocalSession{}
	var late LocalSession
	for _, session := range sessions {
		if session.Path == lateCwd {
			late = session
			continue
		}
		byTitle[session.Title] = session
	}
	if got := byTitle["Upper-case file name"]; got.ID != upperID {
		t.Fatalf("native ID does not match the transcript name: %+v", got)
	}
	if got, ok := byTitle[""]; !ok || got.TitleAuthoritative || got.Directory != project {
		t.Fatalf("untitled session was dropped or confirmed: %+v", sessions)
	}
	if got := byTitle["Most recent prompt"]; got.ID == "" || got.TitleAuthoritative {
		t.Fatalf("fallback differing from the history title was confirmed: %+v", got)
	}
	if got := byTitle["Only prompt"]; !got.TitleAuthoritative {
		t.Fatalf("complete small transcript was provisional: %+v", got)
	}
	if late.Directory != project || late.Title != "" {
		t.Fatalf("cwd beyond the head sample was lost: %+v", late)
	}
	if sessions, err = (ClaudeSessionLister{}).ListSessions(context.Background(), ListSessionsOptions{}); err != nil || len(sessions) != 3 {
		t.Fatalf("SDK-compatible listing kept untitled sessions: %+v %v", sessions, err)
	}
}

func TestClaudeSessionListFiltersAndPaginates(t *testing.T) {
	_, project := claudeListCorpus(t)
	no := false
	source := ClaudeSessionLister{}
	all, err := source.ListSessions(context.Background(), ListSessionsOptions{Directory: project})
	if err != nil || len(all) != 6 {
		t.Fatalf("list corpus: %+v %v", all, err)
	}
	if all[0].Title != "Tail title" || all[1].Title != "Image" || all[2].Title != "Summary title" || all[4].CustomTitle != "Chosen title" || all[4].Tag != "review" {
		t.Fatalf("metadata priority: %+v", all)
	}
	page, err := source.ListSessions(context.Background(), ListSessionsOptions{Directory: project, IncludeProgrammatic: &no, Offset: 2, Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[4].ID {
		t.Fatalf("pagination counted hidden entries: %+v %v", page, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.ListSessions(ctx, ListSessionsOptions{}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestClaudeSessionListContinuationVisibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message map[string]any
		visible bool
	}{
		{"bash", claudeListUser("", "<bash-input>git status</bash-input>"), true},
		{"text", claudeListUser("", "Keep working here"), true},
		{"command", claudeListUser("", "<command-name>/help</command-name>"), false},
		{"meta", claudeListUser("", "<system-reminder>Runtime information</system-reminder>"), false},
		{"tool-result", map[string]any{"type": "user", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_result", "content": "result"},
			map[string]any{"type": "text", "text": "Tool context"},
		}}}, false},
		{"assistant", map[string]any{"type": "assistant", "message": map[string]any{"stop_reason": "end_turn"}}, true},
		{"api-error", map[string]any{"type": "assistant", "isApiErrorMessage": true, "message": map[string]any{"stop_reason": "end_turn"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, project := t.TempDir(), t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", home)
			next := uuid.NewSHA1(uuid.NameSpaceOID, []byte("continuation")).String()
			path := writeClaudeListFixture(t, home, project, "original", 1,
				claudeListUser(project, "Original prompt"),
				map[string]any{"type": "continued-in", "continuedInSessionId": next}, tc.message)
			writeClaudeListFixture(t, home, project, "continuation", 2, claudeListUser(project, "Continued prompt"))
			sessions, err := (ClaudeSessionLister{}).ListSessions(context.Background(), ListSessionsOptions{})
			if err != nil {
				t.Fatal(err)
			}
			visible := false
			for _, session := range sessions {
				visible = visible || session.Path == path
			}
			if visible != tc.visible {
				t.Fatalf("original visibility = %v, want %v: %+v", visible, tc.visible, sessions)
			}
			if sdk := os.Getenv("SPOOL_CLAUDE_SDK_MJS"); sdk != "" {
				assertClaudeSDKParity(t, sdk, ListSessionsOptions{})
			}
		})
	}
}

func TestClaudeSessionListWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	home, repository, linked := t.TempDir(), t.TempDir(), t.TempDir()
	repository, _ = filepath.EvalSymlinks(repository)
	linked, _ = filepath.EvalSymlinks(linked)
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"-c", "core.hooksPath=/dev/null", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "fixture"},
		{"-c", "core.hooksPath=/dev/null", "worktree", "add", "--quiet", "--detach", linked},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repository
		if body, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	writeClaudeListFixture(t, home, repository, "main-checkout", 1, claudeListUser(repository, "Main checkout"))
	writeClaudeListFixture(t, home, linked, "linked-checkout", 2, claudeListUser(linked, "Linked checkout"))
	no := false
	for _, options := range []ListSessionsOptions{{Directory: repository}, {Directory: repository, IncludeWorktrees: &no}} {
		sessions, err := (ClaudeSessionLister{}).ListSessions(context.Background(), options)
		want := 2
		if options.IncludeWorktrees != nil {
			want = 1
		}
		if err != nil || len(sessions) != want {
			t.Fatalf("worktree listing: %+v %v", sessions, err)
		}
		if sdk := os.Getenv("SPOOL_CLAUDE_SDK_MJS"); sdk != "" {
			assertClaudeSDKParity(t, sdk, options)
		}
	}
}

// Run with SPOOL_CLAUDE_SDK_MJS pointing at the pinned npm package extracted
// inside tools/run.sh. Ordinary tests remain offline and require no SDK/runtime.
func TestClaudeSessionListSDKParity(t *testing.T) {
	sdk := os.Getenv("SPOOL_CLAUDE_SDK_MJS")
	if sdk == "" {
		t.Skip("optional SDK parity oracle is not configured")
	}
	home, project := claudeListCorpus(t)
	claudeListEdgeCorpus(t, home, project)
	longProject := filepath.Join(project, strings.Repeat("long-project-", 12))
	if err := os.MkdirAll(longProject, 0700); err != nil {
		t.Fatal(err)
	}
	writeClaudeListFixture(t, home, longProject, "long-project", 50, claudeListUser(longProject, "Long project prompt"))
	no := false
	for _, options := range []ListSessionsOptions{{}, {Directory: project}, {Directory: longProject}, {Limit: 2, Offset: 2}, {Directory: project, IncludeProgrammatic: &no, Limit: 3, Offset: 1}} {
		t.Run(string(mustJSON(t, options)), func(t *testing.T) {
			assertClaudeSDKParity(t, sdk, options)
		})
	}
}

func assertClaudeSDKParity(t *testing.T, sdk string, options ListSessionsOptions) {
	t.Helper()
	params := map[string]any{"dir": options.Directory, "limit": options.Limit, "offset": options.Offset}
	if options.IncludeWorktrees != nil {
		params["includeWorktrees"] = *options.IncludeWorktrees
	}
	if options.IncludeProgrammatic != nil {
		params["includeProgrammatic"] = *options.IncludeProgrammatic
	}
	cmd := exec.Command("bun", "../../tools/claude-session-list-oracle.mjs", sdk, string(mustJSON(t, params)))
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("SDK oracle: %v %s", err, body)
	}
	var expected []map[string]any
	if err := json.Unmarshal(body, &expected); err != nil {
		t.Fatalf("SDK JSON: %v %s", err, body)
	}
	got, err := (ClaudeSessionLister{}).ListSessions(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	actual := []map[string]any{}
	for _, s := range got {
		actual = append(actual, map[string]any{"sessionId": s.ID, "summary": s.Title, "lastModified": float64(s.UpdatedAt), "fileSize": float64(s.FileSize)})
		row := actual[len(actual)-1]
		if s.CreatedAt != 0 {
			row["createdAt"] = float64(s.CreatedAt)
		}
		if s.Directory != "" {
			row["cwd"] = s.Directory
		}
		for key, value := range map[string]string{"customTitle": s.CustomTitle, "firstPrompt": s.FirstPrompt, "gitBranch": s.GitBranch, "tag": s.Tag} {
			if value != "" {
				row[key] = value
			}
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Go/SDK mismatch\nGo: %s\nSDK: %s", mustJSON(t, actual), mustJSON(t, expected))
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
