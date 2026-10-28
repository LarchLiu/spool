package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

func TestTaskChatRejectsAgentForkWithoutChangingItsClaim(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	app, project, _, runner := issueFixture(t)
	if err := app.store.saveAgent(AgentInfo{Backend: chatmodel.BackendClaude, Enabled: true, Installed: true}); err != nil {
		t.Fatal(err)
	}
	chat, err := app.store.createAutomationChat(project.Issues, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "source.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	if err := app.store.SetNativeSessionPath(chat.ID, "task-native", path); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.db.Exec(`UPDATE sessions SET status='completed' WHERE id=?`, chat.ID); err != nil {
		t.Fatal(err)
	}
	ledger, err := issues.Load(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	owner := issues.NewOwner(app.automation.instance, "previous-run")
	owner.Chat = chat.ID
	ledger.Records[1] = &issues.Record{Number: 1, Title: "Fixture", Status: "processing", Owner: owner, Branch: "codex/issue-1-fix"}
	if err := os.MkdirAll(filepath.Dir(ledger.Path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Save(); err != nil {
		t.Fatal(err)
	}
	beforeNative, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeLedger, err := os.ReadFile(ledger.Path)
	if err != nil {
		t.Fatal(err)
	}
	runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
		t.Error("agent started for a rejected task fork")
		return agent.AgentResult{}, errors.New("unexpected task fork")
	}
	if _, err := app.store.forkChat(chat.ID, chatmodel.BackendClaude, chatmodel.CodexOptions{}); !errors.Is(err, errTaskSessionConversion) {
		t.Fatalf("store fork error = %v", err)
	}
	cookie, csrf := loginRequest(t, app, "automation")
	for _, endpoint := range []string{"fork", "messages"} {
		body := `{"backend":"claude"}`
		if endpoint == "messages" {
			body = `{"backend":"claude","message":"continue implementing"}`
		}
		w := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/"+endpoint, body, cookie, csrf)
		if w.Code != 409 || !strings.Contains(w.Body.String(), errTaskSessionConversion.Error()) {
			t.Fatalf("%s: status=%d body=%s", endpoint, w.Code, w.Body.String())
		}
	}
	for file, before := range map[string][]byte{path: beforeNative, ledger.Path: beforeLedger} {
		after, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("rejected conversion modified %s: %v", file, err)
		}
	}
	var chats, runs int
	if err := app.store.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&chats); err != nil || chats != 1 {
		t.Fatalf("rejected conversion created a chat: count=%d error=%v", chats, err)
	}
	if err := app.store.db.QueryRow(`SELECT count(*) FROM automation_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("rejected conversion created a run: count=%d error=%v", runs, err)
	}
	files, err := filepath.Glob(filepath.Join(root, "claude", "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 0 {
		t.Fatal("rejected conversion created native history", files, err)
	}
	latest, ok := app.store.Get(chat.ID)
	if !ok || latest.Backend != chatmodel.BackendCodex || latest.Status != chatmodel.StatusCompleted || latest.NativeSessionID != "task-native" {
		t.Fatal("rejected conversion changed the original chat")
	}

	requests := make(chan agent.AgentRequest, 1)
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		requests <- request
		return agent.AgentResult{}, errors.New("fixture: verification unavailable")
	}
	w := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/messages", `{"backend":"codex","model":"test-model","message":"manual continuation"}`, cookie, csrf)
	if w.Code != 202 {
		t.Fatalf("same-agent continuation rejected: %d %s", w.Code, w.Body.String())
	}
	select {
	case request := <-requests:
		if request.ChatID != chat.ID || request.Model != "test-model" || !strings.Contains(request.Prompt, "attach_artifact()") || !strings.Contains(request.Prompt, "manual continuation") {
			t.Fatal("same-agent continuation lost its claim, model or artifact capability")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("same-agent continuation did not start")
	}
}
