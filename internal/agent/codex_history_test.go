package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexThreadPartsUserImages(t *testing.T) {
	const file = "/tmp/codex-clipboard-screen.png"
	const name = "codex-clipboard-screen.png"
	const image = "data:image/png;base64,aGVsbG8="
	mention := fmt.Sprintf("# Files mentioned by the user:\n\n## %s: %s\nImage attachment: true\n\n## My request:\nReview this screenshot.", name, file)
	text := func(value string) map[string]any { return map[string]any{"type": "text", "text": value} }
	local := map[string]any{"type": "localImage", "path": file}
	for _, test := range []struct {
		name    string
		content []any
		want    []chatmodel.Part
	}{
		{"local image only", []any{local}, []chatmodel.Part{{Role: "user", Kind: "image", Text: "[image]", Name: name, FilePath: file}}},
		{"mentioned local image", []any{text(mention), text("<image name=[Image #1] path=" + strconv.Quote(file) + ">"), local, text("</image>")}, []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "Review this screenshot.\n"},
			{Role: "user", Kind: "image", Text: "[image]", Name: name, FilePath: file},
		}},
		{"mentioned inline image", []any{text(mention), map[string]any{"type": "image", "url": image}}, []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "Review this screenshot."},
			{Role: "user", Kind: "image", Text: "[image]", Name: name, FilePath: file, ImageURL: image},
		}},
		{"mentioned image path", []any{text(mention)}, []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "Review this screenshot."},
			{Role: "user", Kind: "context", Text: name, Name: name, FilePath: file},
		}},
		{"relative local image", []any{map[string]any{"type": "localImage", "path": "screen.png"}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			for i := range test.want {
				test.want[i].Timestamp = 1234
			}
			parts := codexThreadParts([]any{map[string]any{"type": "userMessage", "content": test.content}}, 1234)
			if !reflect.DeepEqual(parts, test.want) {
				t.Fatalf("image projection = %#v, want %#v", parts, test.want)
			}
			if again := projectUserParts(parts); !reflect.DeepEqual(again, parts) {
				t.Fatal("projecting the history twice changed its image attachments")
			}
		})
	}
}

func TestCodexHistoryRPCReadsPagesAndLocatesItemsWithoutResume(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			fixture := newAgentFixture(t, chatmodel.BackendCodex)
			data := map[string]any{"native-thread": map[string]any{
				"thread": map[string]any{"id": "native-thread", "historyMode": "paginated", "name": "Native history"},
				"turns": []any{
					map[string]any{"id": "new", "itemsView": "full", "status": "completed", "startedAt": 20, "items": []any{map[string]any{"type": "commandExecution", "id": "call-new", "command": "cat notes.txt", "status": "completed", "aggregatedOutput": "notes"}}},
					map[string]any{"id": "old", "itemsView": "full", "status": "completed", "startedAt": 10, "items": []any{
						map[string]any{"type": "commandExecution", "id": "call-old", "command": "ls", "status": "failed", "exitCode": 1, "aggregatedOutput": "missing"},
						map[string]any{"type": "reasoning", "id": "thought-old", "summary": []string{"stored reasoning"}},
						map[string]any{"type": "reasoning", "id": "thought-body", "summary": []string{}, "content": []string{"first paragraph", "second paragraph"}},
					}},
				},
			}}
			body, _ := json.Marshal(data)
			if err := os.WriteFile(filepath.Join(fixture.WorkingDirectory, "fixture-thread-history.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			if fallback {
				if err := os.WriteFile(filepath.Join(fixture.WorkingDirectory, "unsupported-item-read"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			history := &CodexHistory{Binary: fixture.Binary}
			defer history.Close()
			ctx := context.Background()
			thread, err := history.Thread(ctx, "native-thread")
			if err != nil || !thread.Paginated {
				t.Fatalf("thread: %+v %v", thread, err)
			}
			newest, err := history.Turns(ctx, "native-thread", "", 1)
			if err != nil || len(newest.Turns) != 1 || newest.Turns[0].ID != "new" || newest.Before == "" {
				t.Fatalf("newest: %+v %v", newest, err)
			}
			oldest, err := history.Turns(ctx, "native-thread", newest.Before, 1)
			if err != nil || len(oldest.Turns) != 1 || oldest.Turns[0].ID != "old" || oldest.Before != "" {
				t.Fatalf("oldest: %+v %v", oldest, err)
			}
			if parts := oldest.Turns[0].Parts; len(parts) != 4 || parts[3].ID != "thought-body" || parts[3].Text != "first paragraph\n\nsecond paragraph" {
				t.Fatalf("reasoning body missing from history: %+v", parts)
			}
			parts, err := history.Item(ctx, "native-thread", "old", "call-old")
			if err != nil || len(parts) != 2 || parts[1].Text != "missing" || parts[0].Native.Status != chatmodel.ToolFailed {
				t.Fatalf("item: %+v %v", parts, err)
			}
			for _, forbidden := range []string{"thread/start", "thread/resume", "turn/start"} {
				if fixtureCount(t, fixture, forbidden) != 0 {
					t.Fatalf("browsing invoked %s", forbidden)
				}
			}
			if fallback && fixtureCount(t, fixture, "thread/items/list") != 1 {
				t.Fatal("missing turn-scoped fallback")
			}
			thought, err := history.Item(ctx, "native-thread", "old", "thought-old")
			if err != nil || len(thought) != 1 || thought[0].ID != "thought-old" || thought[0].Kind != "thinking" || thought[0].Text != "stored reasoning" {
				t.Fatalf("reasoning: %+v %v", thought, err)
			}
			bodyParts, err := history.Item(ctx, "native-thread", "old", "thought-body")
			if err != nil || len(bodyParts) != 1 || bodyParts[0].ID != "thought-body" || bodyParts[0].Kind != "thinking" || bodyParts[0].Text != "first paragraph\n\nsecond paragraph" {
				t.Fatalf("reasoning body detail: %+v %v", bodyParts, err)
			}
		})
	}
}

// Optional: reads real paginated history from the installed app-server in an
// isolated CODEX_HOME. A user shell command makes a turn without a model.
func TestCodexHistoryNativeSmoke(t *testing.T) {
	binary := os.Getenv("SPOOL_CODEX_LIST_SMOKE")
	if binary == "" {
		t.Skip("optional installed app-server smoke is not configured")
	}
	home, directory := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	writer, err := startAgentProcess(ctx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, WorkingDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = writer.stdin.Close()
		cancel()
		<-writer.done
	}()
	if err := writer.initializeCodex(ctx); err != nil {
		t.Fatal(err)
	}
	started, err := writer.codexRPC(ctx, "thread/start", map[string]any{"cwd": directory, "approvalPolicy": "on-request", "sandbox": "workspace-write"})
	if err != nil {
		t.Fatal(err)
	}
	thread := nestedString(started, "thread", "id")
	for _, command := range []string{"cat notes.txt | head -1; ls", "ls /definitely-missing-spool-smoke", "sed -n '1,2p' notes.txt"} {
		if _, err := writer.codexRPC(ctx, "thread/shellCommand", map[string]any{"threadId": thread, "command": command}); err != nil {
			t.Fatal(err)
		}
		for {
			value, err := writer.read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if value["method"] == "turn/completed" {
				break
			}
		}
	}
	// The writer still owns the thread: a viewer must be able to read it anyway.
	history := &CodexHistory{Binary: binary}
	defer history.Close()
	info, err := history.Thread(ctx, thread)
	if err != nil || !info.Paginated || info.Path == "" {
		t.Fatalf("thread: %+v %v", info, err)
	}
	before, err := os.Stat(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	newest, err := history.Turns(ctx, thread, "", 2)
	if err != nil || len(newest.Turns) != 2 || newest.Before == "" {
		t.Fatalf("newest page: %+v %v", newest, err)
	}
	oldest, err := history.Turns(ctx, thread, newest.Before, 2)
	if err != nil || len(oldest.Turns) != 1 || oldest.Before != "" {
		t.Fatalf("oldest page: %+v %v", oldest, err)
	}
	turns := append(oldest.Turns, newest.Turns...)
	located, err := history.Item(ctx, thread, turns[0].ID, turns[0].Parts[0].ID)
	if err != nil || len(located) != 2 || located[1].Text != turns[0].Parts[1].Text {
		t.Fatalf("targeted item: %+v %v", located, err)
	}
	for i, expected := range []struct {
		status     string
		exit       int
		kind       string
		operations int
	}{{chatmodel.ToolCompleted, 0, "shell", 2}, {chatmodel.ToolFailed, 1, "list", 0}, {chatmodel.ToolCompleted, 0, "read", 0}} {
		turn := turns[i]
		if turn.Status != chatmodel.StatusCompleted || turn.StartedAt == 0 || len(turn.Parts) != 2 {
			t.Fatalf("turn %d: %+v", i, turn)
		}
		use, result := turn.Parts[0], turn.Parts[1]
		native := use.Native
		if use.Kind != "tool_use" || result.Kind != "tool_result" || use.ID == "" || use.ID != result.ID || native == nil || native.Status != expected.status || native.ExitCode == nil || *native.ExitCode != expected.exit || native.Kind != expected.kind || len(native.Operations) != expected.operations {
			t.Fatalf("turn %d item: %+v native %+v", i, turn.Parts, native)
		}
	}
	if after, err := os.Stat(info.Path); err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("reading history changed the rollout")
	}
	if _, err := history.Thread(ctx, "01a1d000-0000-7000-8000-000000000000"); err == nil {
		t.Fatal("an unknown thread was read")
	}
}
