package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

const claudeReviewReport = "Review complete.\n\n1. **Keep command results visible** — render `<config>` & flags.\n2. **Keep the complete report** — reopening history must preserve both findings."

func TestClaudeLocalCommandHistoryPreservesReviewResultAndCompletion(t *testing.T) {
	path := "testdata/claude-review.jsonl"
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := IndexSessionTurns("claude", path, 0)
	if err != nil || len(turns) != 1 {
		t.Fatalf("review history boundaries: %+v, %v", turns, err)
	}
	turn := turns[0]
	if turn.Status != chatmodel.StatusCompleted || turn.FinishedAt != nativeTimestamp("2026-11-25T00:20:34.000Z") || turn.ID != "review-input" || !turn.HasUserInput() {
		t.Fatalf("local command did not finish its turn: %+v", turn)
	}
	check := func(transcript chatmodel.Transcript, err error) {
		t.Helper()
		if err != nil || len(transcript.Parts) != 2 || transcript.Parts[0].Role != "user" || transcript.Parts[0].Text != "/code-review" || transcript.Parts[1].Role != "assistant" || transcript.Parts[1].Text != claudeReviewReport || transcript.Parts[1].Phase != "final_answer" {
			t.Fatalf("local review result was hidden or changed: %+v, %v", transcript, err)
		}
	}
	check(ReadSessionTurn("claude", path, turn))
	check(ParseSessionTranscript("claude", path))
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatal("display modified the native session")
	}
	continued := filepath.Join(t.TempDir(), "continued.jsonl")
	if err := os.WriteFile(continued, before, 0600); err != nil {
		t.Fatal(err)
	}
	writeTitleRecords(t, continued,
		map[string]any{"type": "user", "uuid": "next-input", "parentUuid": "review-report", "message": map[string]any{"role": "user", "content": "Continue"}},
		map[string]any{"type": "assistant", "uuid": "next-answer", "parentUuid": "next-input", "message": map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": "Continued"}},
	)
	turns, err = IndexSessionTurns("claude", continued, 0)
	if err != nil || len(turns) != 2 {
		t.Fatalf("continuation swallowed the review: %+v, %v", turns, err)
	}
	check(ReadSessionTurn("claude", continued, turns[0]))
}

func TestClaudeLocalCommandOutputUnwrapsOnlyProviderRecords(t *testing.T) {
	for _, test := range []struct{ name, raw, want string }{
		{"stdout", "<local-command-stdout>**Report** &lt;T&gt; &amp; flags</local-command-stdout>", "**Report** <T> & flags"},
		{"stderr", "<local-command-stderr>Review failed</local-command-stderr>", "Review failed"},
		{"both", "<local-command-stdout>Partial report</local-command-stdout>\n<local-command-stderr>Warning</local-command-stderr>", "Partial report\nWarning"},
		{"one decode", "<local-command-stdout>&amp;lt;literal&amp;gt;</local-command-stdout>", "&lt;literal&gt;"},
		{"plain", "Legacy report", "Legacy report"},
		{"empty", "<local-command-stdout>  </local-command-stdout>", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := map[string]any{"type": "system", "subtype": "local_command", "uuid": "output", "isMeta": true, "content": test.raw}
			parts := claudeSessionParts(record)
			if test.want == "" {
				if len(parts) != 0 {
					t.Fatal("empty command output displayed", parts)
				}
				return
			}
			if len(parts) != 1 || parts[0].Text != test.want || parts[0].Role != "assistant" || parts[0].Phase != "final_answer" {
				t.Fatalf("lost command output: %+v", parts)
			}
			for _, flag := range []string{"isSidechain", "isCompactSummary"} {
				record[flag] = true
				if parts := claudeSessionParts(record); len(parts) != 0 {
					t.Fatalf("%s exposed internal output: %+v", flag, parts)
				}
				delete(record, flag)
			}
			if parts := claudeSessionParts(map[string]any{"type": "user", "message": map[string]any{"content": test.raw}}); test.name != "plain" && len(parts) != 0 {
				t.Fatalf("user command wrapper exposed as a reply: %+v", parts)
			}
		})
	}
}

func TestClaudeLocalCommandStreamShowsResultBeforeTerminalEvent(t *testing.T) {
	for _, mode := range []string{"system", "synthetic", "synthetic-blocks", "both", "synthetic-first"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			p := &agentProcess{initialized: true, sessionID: NewSteerID(), permissionMode: chatmodel.DefaultPermissionMode("claude"), ctx: ctx, stdin: &approvalWriter{}, messages: make(chan map[string]any, 1)}
			raw := "<local-command-stdout>**Review complete.** &lt;config&gt; &amp; flags</local-command-stdout>"
			want := "**Review complete.** <config> & flags"
			system := map[string]any{"type": "system", "subtype": "local_command", "uuid": "report", "content": raw}
			synthetic := map[string]any{"type": "assistant", "uuid": "report", "local_command_source": raw, "message": map[string]any{"id": "synthetic-report", "model": "<synthetic>", "stop_reason": "end_turn", "content": want}}
			switch mode {
			case "system":
				p.queued = []map[string]any{system}
			case "synthetic":
				p.queued = []map[string]any{synthetic}
			case "synthetic-blocks":
				synthetic["message"].(map[string]any)["content"] = []any{map[string]any{"type": "text", "text": want}}
				synthetic["local_command_source"] = "code-review"
				p.queued = []map[string]any{synthetic}
			case "both":
				p.queued = []map[string]any{system, synthetic}
			case "synthetic-first":
				p.queued = []map[string]any{synthetic, system}
			}
			var text strings.Builder
			sentResult := false
			result, err, reusable := p.runClaude(ctx, AgentRequest{Backend: "claude", Prompt: "Review changes"}, func(event AgentEvent) error {
				if event.Type == "delta" {
					if event.Part == nil || event.Part.Phase != "final_answer" {
						t.Errorf("command result has no final-answer phase: %+v", event)
					}
					text.WriteString(event.Text)
					if !sentResult {
						sentResult = true
						p.messages <- map[string]any{"type": "result", "subtype": "success", "result": want}
					}
				}
				return nil
			})
			if err != nil || !reusable || !sentResult || result.Text != want || text.String() != want {
				t.Fatalf("command result missing or repeated: result=%+v deltas=%q err=%v", result, text.String(), err)
			}
		})
	}
}

func TestClaudeLocalCommandHistoryDeduplicatesTwinsButKeepsLaterReports(t *testing.T) {
	for _, syntheticFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "native-first", true: "synthetic-first"}[syntheticFirst], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "review.jsonl")
			native := map[string]any{"type": "system", "subtype": "local_command", "uuid": "report", "parentUuid": "input", "content": "<local-command-stdout>Report &amp;lt;literal&amp;gt;</local-command-stdout>"}
			synthetic := map[string]any{"type": "assistant", "uuid": "report", "parentUuid": "input", "local_command_source": native["content"], "message": map[string]any{"model": "<synthetic>", "content": []any{map[string]any{"type": "text", "text": "Report &lt;literal&gt;"}}}}
			first, second := native, synthetic
			if syntheticFirst {
				first, second = second, first
			}
			writeTitleRecords(t, path,
				map[string]any{"type": "user", "uuid": "input", "message": map[string]any{"content": "/code-review"}}, first, second,
				map[string]any{"type": "user", "uuid": "next-input", "parentUuid": "report", "message": map[string]any{"content": "/code-review"}},
				map[string]any{"type": "system", "subtype": "local_command", "uuid": "next-report", "parentUuid": "next-input", "content": native["content"]},
			)
			transcript, err := ParseSessionTranscript("claude", path)
			if err != nil || len(transcript.Parts) != 4 || transcript.Parts[1].Text != "Report &lt;literal&gt;" || transcript.Parts[3].Text != transcript.Parts[1].Text {
				t.Fatalf("duplicated a twin or suppressed a distinct report: %+v, %v", transcript, err)
			}
		})
	}
}

func TestClaudeLocalCommandDelayedReplyKeepsItsOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.jsonl")
	writeTitleRecords(t, path,
		map[string]any{"type": "user", "uuid": "review", "message": map[string]any{"content": "/code-review"}},
		map[string]any{"type": "user", "uuid": "next", "parentUuid": "review", "message": map[string]any{"content": "Continue"}},
		map[string]any{"type": "system", "subtype": "local_command", "uuid": "report", "parentUuid": "review", "timestamp": "2026-11-25T00:20:34.000Z", "content": "<local-command-stdout>Review report</local-command-stdout>"},
		map[string]any{"type": "assistant", "parentUuid": "next", "message": map[string]any{"content": "Continued", "stop_reason": "end_turn"}},
	)
	turns, err := IndexSessionTurns("claude", path, 0)
	if err != nil || len(turns) != 2 || turns[0].Status != chatmodel.StatusCompleted || turns[0].FinishedAt != nativeTimestamp("2026-11-25T00:20:34.000Z") {
		t.Fatalf("delayed report lost its turn: %+v, %v", turns, err)
	}
	for i, want := range []string{"Review report", "Continued"} {
		transcript, err := ReadSessionTurn("claude", path, turns[i])
		if err != nil || len(transcript.Parts) != 2 || transcript.Parts[1].Text != want {
			t.Fatalf("report attached to wrong turn: %+v, %v", transcript, err)
		}
	}
}
