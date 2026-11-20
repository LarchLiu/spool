package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestLazyTaskSnapshotKeepsEmptyTranscriptArray(t *testing.T) {
	// A fork can emit task events before a native user record exists. An
	// empty lazy snapshot must keep the same array contract as other frames.
	data, err := json.Marshal(slimToolDetails(chatmodel.Session{Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{}}}))
	if err != nil || !strings.Contains(string(data), `"parts":[]`) {
		t.Fatalf("empty transcript changed into null: %s (%v)", data, err)
	}
}

func TestTaskTitleAndSummaryNeverClassifyItsRow(t *testing.T) {
	diff := "Inspect\ndiff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n+12 -3"
	for _, title := range []string{"Think", "patch", "fix.patch", "bash", "Read config.json"} {
		task := &chatmodel.TaskProgress{ID: "child"}
		part := chatmodel.Part{ID: "task", Role: "assistant", Kind: "tool_use", Name: title, Text: title + "\n" + diff,
			Native: &chatmodel.ToolNative{Kind: "task", Status: "stopped", Task: task}}
		summary := summarizeTool(part)
		if summary.Kind != "task" || summary.Status != "stopped" || summary.Task != task || summary.Changes != nil || summary.Stats != "" || summary.Path != "" || len(summary.Operations) != 0 {
			t.Fatalf("task %q summarised by its title or text: %+v", title, summary)
		}
	}
}

func TestClaudeForkedReviewUpdatesLiveTaskBeforeFinalReply(t *testing.T) {
	localSessionHomes(t)
	fixture := newAgentFixture(t, chatmodel.BackendClaude)
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{chatmodel.BackendClaude: fixture.Binary})
	s := testStore(t)
	chat, err := s.createSession("", "", chatmodel.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	var statuses []string
	result, err := a.runTurn(context.Background(), chat.ID, "/code-review", func(event agent.AgentEvent) error {
		if event.Part == nil || event.Part.Kind != "tool_use" {
			return nil
		}
		a.liveMu.Lock()
		turn := a.live[chat.ID]
		a.liveMu.Unlock()
		if turn.Reply != "" {
			t.Error("task event completed the chat before the report")
		}
		var tasks []chatmodel.Part
		for _, part := range turn.Parts {
			if part.Kind == "tool_use" {
				tasks = append(tasks, part)
			}
		}
		if len(tasks) != 1 || tasks[0].Name != "/code-review" || tasks[0].Tool == nil {
			t.Fatalf("progress was hidden or appended a duplicate row: %#v", tasks)
		}
		task := tasks[0]
		statuses = append(statuses, task.Tool.Status)
		if event.Type == "tool" && !strings.Contains(task.Text, "Tool uses:") {
			t.Error("live row did not receive native progress", task)
		}
		// Progress comes from the wire, so it must remain usable before the
		// native fork writes a corresponding top-level transcript record.
		projected := slimToolDetails(chatmodel.Session{ID: chat.ID, Turns: []chatmodel.Turn{{ID: turn.ID, PartEnd: 1}}, Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{task}}}).Transcript.Parts[0]
		if projected.Text != task.Text || projected.Tool.Href != "" {
			t.Error("task progress depends on unavailable native history", projected)
		}
		return nil
	})
	if err != nil || result.Text != "Hello world" || strings.Join(statuses, ",") != "running,running,completed" {
		t.Fatalf("review lost its progress/report: result=%#v statuses=%v error=%v", result, statuses, err)
	}
}

func TestGenericLiveTasksSurviveSnapshotAndRemainIndependent(t *testing.T) {
	for _, backend := range []chatmodel.Backend{"claude", "codex"} {
		t.Run(string(backend), func(t *testing.T) {
			localSessionHomes(t)
			fixture := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: fixture.Binary})
			s := testStore(t)
			chat, err := s.createSession("", "", backend)
			if err != nil {
				t.Fatal(err)
			}
			a := NewApp(s, runner, "", "")
			t.Cleanup(func() { _ = a.Close() })
			seenSnapshot := false
			result, err := a.runTurn(t.Context(), chat.ID, "subagent-live", func(event agent.AgentEvent) error {
				if event.Type != "delta" || event.Text != "Main report" {
					return nil
				}
				current, ok := s.Get(chat.ID)
				if !ok {
					t.Fatal("chat missing")
				}
				snapshot := slimToolDetails(a.snapshot(current))
				tasks := 0
				for _, part := range snapshot.Transcript.Parts {
					if part.Tool == nil || part.Tool.Kind != "task" {
						continue
					}
					tasks++
					if part.Tool.Task == nil || part.Tool.Task.ID == "" || part.Tool.Status != "running" || part.Tool.Href != "" || part.Text == "" {
						t.Fatal("refresh lost inline child progress", part)
					}
				}
				if tasks != 2 {
					t.Fatalf("snapshot has %d children", tasks)
				}
				seenSnapshot = true
				return nil
			})
			// Claude Code answers its children's notifications with one more turn.
			want := "Main report"
			if backend == chatmodel.BackendClaude {
				want += " Children finished"
			}
			if err != nil || result.Text != want || !seenSnapshot {
				t.Fatal(result, err, seenSnapshot)
			}
			a.liveMu.Lock()
			finished := a.completed[chat.ID].turn
			a.liveMu.Unlock()
			count := 0
			for _, part := range finished.Parts {
				if part.Tool != nil && part.Tool.Kind == "task" {
					count++
					if part.Tool.Status != "completed" || part.Tool.Task.Summary == "" {
						t.Fatal("missing child outcome", part)
					}
				}
			}
			if count != 2 {
				t.Fatal("duplicate or missing live task", count)
			}
		})
	}
}

func TestCancelledLiveTasksDoNotKeepRunningInTerminalSnapshot(t *testing.T) {
	for _, backend := range []chatmodel.Backend{"claude", "codex"} {
		t.Run(string(backend), func(t *testing.T) {
			localSessionHomes(t)
			fixture := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: fixture.Binary})
			s := testStore(t)
			chat, err := s.createSession("", "", backend)
			if err != nil {
				t.Fatal(err)
			}
			a := NewApp(s, runner, "", "")
			t.Cleanup(func() { _ = a.Close() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err = a.runTurn(ctx, chat.ID, "subagent-live block", func(e agent.AgentEvent) error {
				if e.Type == "delta" {
					cancel()
				}
				return nil
			})
			if err == nil {
				t.Fatal("cancelled background wait succeeded")
			}
			a.liveMu.Lock()
			terminal := a.completed[chat.ID].turn
			a.liveMu.Unlock()
			count := 0
			for _, part := range terminal.Parts {
				if part.Tool != nil && part.Tool.Kind == "task" {
					count++
					if part.Tool.Status != "stopped" {
						t.Fatal("cancelled child still running", part)
					}
				}
			}
			if count != 2 {
				t.Fatal("terminal snapshot lost tasks", count)
			}
		})
	}
}

func TestTaskStepsAreSummarizedLikeTheCallsTheyAre(t *testing.T) {
	task := &chatmodel.TaskProgress{ID: "child", Steps: []chatmodel.TaskStep{
		{Kind: chatmodel.TaskStepMessage, Text: "Reading the grammar"},
		{ID: "read", Name: "Read", Input: `{"file_path":"/repo/internal/parser/grammar.go"}`, Status: "completed"},
		{ID: "grep", Name: "Grep", Input: `{"path":"src","pattern":"TODO"}`, Status: "failed"},
		{ID: "bash", Name: "Bash", Input: "{\"command\":\"go test ./...\\n\"}", Status: "running"},
		// Codex's own reading of a command outranks the literal one.
		{ID: "cat", Name: "shell", Input: "sed -n 1,20p child.go", Kind: "read", Path: "child.go", Status: "completed"},
		{ID: "write", Name: "files.write_file", Input: `{"path":"/work/output.txt"}`},
		{ID: "image", Name: "view_image", Input: `{"path":"/work/page 1.png"}`},
		{Name: "mcp__tracker__create_issue", Input: `{"title":"Parser"}`},
	}}
	steps := func(status string) string {
		part := chatmodel.Part{ID: "task", Role: "assistant", Kind: "tool_use", Name: "Inspect parser", Text: "Inspect parser",
			Native: &chatmodel.ToolNative{Kind: "task", Status: status, Task: task}}
		data, err := json.Marshal(summarizeTool(part).Task.Steps)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	// Call identities and inputs stay on the server: a client gets the reading.
	want := `[{"kind":"message","text":"Reading the grammar"},` +
		`{"kind":"read","name":"Read","path":"…/internal/parser/grammar.go","status":"completed"},` +
		`{"kind":"search","name":"Grep","path":"src","query":"TODO","status":"failed"},` +
		`{"kind":"shell","name":"Bash","text":"go test ./...","status":"running"},` +
		`{"kind":"read","name":"shell","path":"child.go","status":"completed"},` +
		`{"kind":"create","name":"files.write_file","path":"/work/output.txt"},` +
		`{"kind":"read","name":"view_image","path":"/work/page 1.png"},` +
		`{"kind":"other","name":"mcp__tracker__create_issue"}]`
	if got := steps("running"); got != want {
		t.Fatalf("steps summarized as\n%s\nwant\n%s", got, want)
	}
	// Nothing told how a call ended when its child stopped first.
	if got, want := steps("stopped"), strings.Replace(want, `,"status":"running"`, "", 1); got != want {
		t.Fatalf("stopped child's steps summarized as\n%s\nwant\n%s", got, want)
	}
	if task.Steps[1].Kind != "" || task.Steps[3].Status != "running" {
		t.Fatal("summary changed the live snapshot", task.Steps)
	}
}
