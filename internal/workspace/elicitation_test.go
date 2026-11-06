package workspace

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type elicitationRunner struct {
	fakeRunner
	response chan chatmodel.ApprovalResponse
}

func (r *elicitationRunner) Run(ctx context.Context, request agent.AgentRequest, _ func(agent.AgentEvent) error) (agent.AgentResult, error) {
	response, err := request.Approve(ctx, chatmodel.ApprovalRequest{Title: "Computer Use", Reason: "Allow Calculator?", Fields: []chatmodel.ApprovalField{
		{Name: "confirm", Label: "Confirm", Type: "boolean", Required: true},
	}})
	if err == nil {
		r.response <- response
	}
	return agent.AgentResult{Text: "completed"}, err
}

func TestMCPElicitationHTTPValidatesContentBeforeResolving(t *testing.T) {
	s := testStore(t)
	user := testUser(t, s, "alice")
	project := testProject(t, s, user)
	testAgent(t, s)
	chat, _ := s.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	runner := &elicitationRunner{response: make(chan chatmodel.ApprovalResponse, 1)}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	if w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"Use Calculator","permission_mode":"auto"}`, cookie, csrf); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var turn chatmodel.Turn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		saved, _ := s.Get(chat.ID)
		snapshot := a.snapshot(saved)
		if len(snapshot.Turns) > 0 {
			turn = snapshot.Turns[0]
			if turn.PendingApproval != nil {
				break
			}
		}
		time.Sleep(time.Millisecond)
	}
	if turn.PendingApproval == nil || len(turn.PendingApproval.Fields) != 1 || turn.PendingApproval.Reason != "Allow Calculator?" {
		t.Fatalf("MCP form not present in the live snapshot: %#v", turn)
	}
	path := "/api/chats/" + chat.ID + "/approvals"
	for _, content := range []map[string]any{nil, {"confirm": "true"}, {"confirm": true, "persist": "always"}} {
		body, _ := json.Marshal(map[string]any{"id": turn.PendingApproval.ID, "turn_id": turn.ID, "allow": true, "content": content})
		if w := apiRequest(a, "POST", path, string(body), cookie, csrf); w.Code != 400 {
			t.Fatalf("invalid form resolved approval: %d %s", w.Code, w.Body.String())
		}
	}
	content := map[string]any{"confirm": false}
	body, _ := json.Marshal(map[string]any{"id": turn.PendingApproval.ID, "turn_id": turn.ID, "allow": true, "content": content})
	if w := apiRequest(a, "POST", path, string(body), cookie, csrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	select {
	case response := <-runner.response:
		if !response.Allow || !reflect.DeepEqual(response.Content, content) {
			t.Fatalf("content did not reach the provider: %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("valid response did not resume the provider")
	}
	if w := apiRequest(a, "POST", path, string(body), cookie, csrf); w.Code != 409 {
		t.Fatal("approval accepted twice")
	}
	if saved := waitChat(t, s, chat.ID); saved.Status != chatmodel.StatusCompleted {
		t.Fatal("provider did not finish after approval")
	}
	a.liveMu.Lock()
	for _, action := range a.live[chat.ID].Actions {
		if action.Summary != "Computer Use · Allow Calculator?" {
			t.Errorf("live audit summary=%q", action.Summary)
		}
	}
	a.liveMu.Unlock()
	var summary string
	if err := s.db.QueryRow(`SELECT summary FROM turn_actions WHERE session_id=? AND approval_id=?`, chat.ID, turn.PendingApproval.ID).Scan(&summary); err != nil || summary != "Computer Use · Allow Calculator?" {
		t.Fatalf("approval summary was not saved: %q %v", summary, err)
	}
}
