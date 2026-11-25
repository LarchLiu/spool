package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

// observeAgentExchanges reports every agent message A sends to B. A non-nil
// error from inspect replaces B's answer after B has handled the message.
func observeAgentExchanges(f remoteFixture, inspect func(body []byte) error) {
	dial := f.a.federationDial
	f.a.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
		answer, err := dial(ctx, peer, body)
		if bytes.Contains(body, []byte(`"action":"agent"`)) {
			if lost := inspect(body); lost != nil {
				return nil, lost
			}
		}
		return answer, err
	}
}

func TestRemoteAgentWatchingATaskNeitherLogsInNorResendsTheMessage(t *testing.T) {
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	f := remoteFixtureForTest(t, runner)
	// The first access provisions the remote account through the ordinary login.
	f.call(t, "projects", federationAgentPayload{})
	revision := func() (value int) {
		t.Helper()
		if err := f.b.store.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key='profile_revision'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := revision()
	changes := f.b.store.changes.subscribe()
	defer changes.close()
	var exchanges atomic.Int32
	var repeated atomic.Bool
	observeAgentExchanges(f, func(body []byte) error {
		if exchanges.Add(1) > 1 && bytes.Contains(body, []byte("Task text sent once")) {
			repeated.Store(true)
		}
		return nil
	})
	data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{Server: f.peer, WaitSeconds: 2, federationAgentPayload: federationAgentPayload{
		Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Message: "Task text sent once", Context: "Context sent once", RequestID: chatmodel.NewID()}})
	var task remoteAgentTask
	if err != nil || json.Unmarshal(data, &task) != nil || task.Status != "running" {
		t.Fatal("waiting delegation", string(data), err)
	}
	// Two seconds of 250 ms polls took nine exchanges; the target now holds one query open.
	if count := exchanges.Load(); count > 3 {
		t.Fatal("waiting polled the target instead of holding one query open", count)
	}
	if repeated.Load() {
		t.Fatal("task queries carried the delegated message again")
	}
	close(runner.release)
	if data := f.call(t, "task", federationAgentPayload{TaskID: task.ID}); !bytes.Contains(data, []byte(`"status":"completed"`)) {
		t.Fatal("held query missed the completion", string(data))
	}
	if after := revision(); after != before {
		t.Fatal("agent operations rewrote the remote profile", before, after)
	}
	var sessions int
	if err := f.b.store.db.QueryRow(`SELECT count(*) FROM auth_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("agent operations left remote login sessions", sessions, err)
	}
	if changes.take()&changeProfiles != 0 {
		t.Fatal("agent operations announced a profile change to every client")
	}
}

func TestRemoteAgentDelegationRecoversFromALostAnswer(t *testing.T) {
	runner := &fakeRunner{text: "ran once"}
	f := remoteFixtureForTest(t, runner)
	lose := 1
	observeAgentExchanges(f, func(body []byte) error {
		if bytes.Contains(body, []byte(`"operation":"delegate"`)) && lose > 0 {
			lose--
			return errors.New("answer lost in transit")
		}
		return nil
	})
	request := remoteAgentOperation{Server: f.peer, WaitSeconds: 2, federationAgentPayload: federationAgentPayload{Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Message: "Do work", RequestID: chatmodel.NewID()}}
	data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request)
	if err != nil || !bytes.Contains(data, []byte("ran once")) {
		t.Fatal("delegation was lost with its answer", string(data), err)
	}
	if calls := f.call(t, "calls", federationAgentPayload{}); !bytes.Contains(calls, []byte("ran once")) {
		t.Fatal("recovered task has no local receipt", string(calls))
	}
	// When no answer arrives at all, the caller learns how to recover the task.
	lose = 2
	request.RequestID = chatmodel.NewID()
	if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request); err == nil || !strings.Contains(err.Error(), request.RequestID) {
		t.Fatal("unconfirmed delegation did not name its request ID", err)
	}
	data, err = f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request)
	if err != nil || !bytes.Contains(data, []byte("ran once")) {
		t.Fatal("retry did not recover the unconfirmed task", string(data), err)
	}
	f.b.turns.Wait()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.requests) != 2 {
		t.Fatal("lost answers executed a delegation more than once", len(runner.requests))
	}
}

func TestRemoteAgentRefusedStartLeavesNothingBehindAndCanBeRetried(t *testing.T) {
	gate := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	f := remoteFixtureForTest(t, gate)
	request := remoteAgentOperation{Server: f.peer, federationAgentPayload: federationAgentPayload{Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Message: "First", RequestID: chatmodel.NewID()}}
	updating := func(value bool) {
		f.b.turnMu.Lock()
		f.b.updating = value
		f.b.turnMu.Unlock()
	}
	updating(true)
	if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request); err == nil {
		t.Fatal("delegation started while the target was updating")
	}
	var sessions, receipts int
	if err := f.b.store.db.QueryRow(`SELECT (SELECT count(*) FROM sessions),(SELECT count(*) FROM federation_agent_tasks)`).Scan(&sessions, &receipts); err != nil || sessions != 0 || receipts != 0 {
		t.Fatal("refused delegation left a conversation or receipt", sessions, receipts, err)
	}
	updating(false)
	data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request)
	var first remoteAgentTask
	if err != nil || json.Unmarshal(data, &first) != nil || first.Status != "running" {
		t.Fatal("same request ID could not run after the refusal", string(data), err)
	}
	<-gate.started
	// A follow-up cannot start while the conversation is busy. Nothing ran for
	// it, so its request ID must run later rather than replay the refusal.
	followup := request
	followup.Message, followup.RequestID, followup.Conversation = "Second", chatmodel.NewID(), first.Conversation
	data, err = f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", followup)
	var refused remoteAgentTask
	if err != nil || json.Unmarshal(data, &refused) != nil || refused.Status != "failed" || refused.TurnID != "" {
		t.Fatal("busy conversation accepted a second turn", string(data), err)
	}
	close(gate.release)
	f.b.turns.Wait()
	f.b.runner = &fakeRunner{text: "second result"}
	followup.WaitSeconds = 2
	data, err = f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", followup)
	var retried remoteAgentTask
	if err != nil || json.Unmarshal(data, &retried) != nil || retried.Status != "completed" || retried.Text != "second result" || retried.ID != refused.ID || retried.Conversation != first.Conversation {
		t.Fatal("refused request ID replayed its failure", string(data), err)
	}
	if err := f.b.store.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatal("retry created another conversation", sessions, err)
	}
}

// The first permission probe blocks, as a slow native CLI would.
type slowDiscoveryRunner struct {
	fakeRunner
	once             sync.Once
	entered, release chan struct{}
}

func (r *slowDiscoveryRunner) PermissionModes(ctx context.Context, backend chatmodel.Backend, directory string) (chatmodel.PermissionCatalog, error) {
	first := false
	r.once.Do(func() { first = true })
	if first {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return chatmodel.PermissionCatalog{}, ctx.Err()
		}
	}
	return r.fakeRunner.PermissionModes(ctx, backend, directory)
}

func TestRemoteAgentSlowDiscoveryDoesNotBlockOtherDelegations(t *testing.T) {
	runner := &slowDiscoveryRunner{fakeRunner: fakeRunner{text: "done"}, entered: make(chan struct{}), release: make(chan struct{})}
	f := remoteFixtureForTest(t, runner)
	delegate := func() (remoteAgentTask, error) {
		data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{Server: f.peer, WaitSeconds: 2, federationAgentPayload: federationAgentPayload{
			Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Message: "Work", RequestID: chatmodel.NewID()}})
		var task remoteAgentTask
		if err == nil {
			err = json.Unmarshal(data, &task)
		}
		return task, err
	}
	slow := make(chan error, 1)
	go func() {
		task, err := delegate()
		if err == nil && task.Status != "completed" {
			err = errors.New("slow delegation ended as " + task.Status)
		}
		slow <- err
	}()
	<-runner.entered
	finished := make(chan error, 1)
	go func() {
		task, err := delegate()
		if err == nil && task.Status != "completed" {
			err = errors.New("delegation ended as " + task.Status)
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another delegation waited for a slow permission probe")
	}
	close(runner.release)
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}

func TestAgentMCPKeepsPermissionChoiceAndRequestSizeOutOfTheModelsHands(t *testing.T) {
	runner := &fakeRunner{text: "MCP result"}
	f := remoteFixtureForTest(t, runner)
	config, release, err := f.a.agentMCPGrant(t.Context(), f.identity, f.origin, "parent-turn")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	call := func(arguments map[string]any) (int, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "spool_delegate", "arguments": arguments}})
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, config.URL, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+config.Token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var answer bytes.Buffer
		_, _ = answer.ReadFrom(response.Body)
		return response.StatusCode, answer.String()
	}
	arguments := func(message string) map[string]any {
		return map[string]any{"server": f.peer, "project": f.project, "agent": "codex", "message": message, "request_id": chatmodel.NewID(), "wait_seconds": 2}
	}
	for _, tool := range remoteAgentTools() {
		if _, offered := tool.InputSchema["properties"].(map[string]any)["permission_mode"]; offered {
			t.Fatal("MCP offers the model a permission mode", tool.Name)
		}
	}
	unsafe := arguments("Do work")
	unsafe["permission_mode"] = "danger-full-access"
	if _, answer := call(unsafe); !strings.Contains(answer, "Unknown tool argument: permission_mode") {
		t.Fatal("model chose the remote permission mode", answer)
	}
	runner.mu.Lock()
	ran := len(runner.requests)
	runner.mu.Unlock()
	if ran != 0 {
		t.Fatal("refused delegation ran")
	}
	// A message near its documented limit is a valid request, with JSON escaping.
	large := strings.Repeat("line \"quoted\"\n", chatmodel.MaxMessageBytes/14-1)
	if status, answer := call(arguments(large)); status != http.StatusOK || !strings.Contains(answer, "MCP result") {
		t.Fatal("documented message size was refused", status, chatmodel.Clip(answer, 300))
	}
	status, answer := call(arguments(strings.Repeat("x", remoteAgentMaxBody)))
	if status != http.StatusRequestEntityTooLarge || !strings.Contains(answer, "too large") || strings.Contains(answer, "Invalid JSON-RPC") {
		t.Fatal("oversized request was reported as malformed JSON", status, answer)
	}
}
