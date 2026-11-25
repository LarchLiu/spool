package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

type remoteFixture struct {
	a, b                  *App
	identity              authIdentity
	peer, project, origin string
}

func remoteFixtureForTest(t *testing.T, runner agent.AgentRunner) remoteFixture {
	t.Helper()
	sa, sb := testStore(t), testStore(t)
	u := testUser(t, sa, "remote-user")
	project := testProject(t, sb)
	localProject := testProject(t, sa, u)
	chat, err := sa.CreateChat(u.ID, localProject.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	a, b := NewApp(sa, &fakeRunner{}, "", ""), NewApp(sb, runner, "", "")
	as, bs := httptest.NewTLSServer(a), httptest.NewTLSServer(b)
	t.Cleanup(as.Close)
	t.Cleanup(bs.Close)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	for app, remote := range map[*App]*httptest.Server{a: bs, b: as} {
		app.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
			return federationHTTP(ctx, peer.Address, body, remote.Client().Transport.(*http.Transport).Clone())
		}
	}
	peer := federationPairForTest(t, sa, sb, bs.URL, false, []string{u.ID}, nil)
	federationPairForTest(t, sb, sa, as.URL, true, nil, []FederationProject{{project.ID, "control", "create"}})
	testAgent(t, sb)
	_, identity, err := sa.login(u.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	return remoteFixture{a, b, identity, peer.ID, project.ID, chat.ID}
}

func (f remoteFixture) call(t *testing.T, operation string, request federationAgentPayload) json.RawMessage {
	t.Helper()
	request.Operation = operation
	body, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{federationAgentPayload: request, Server: f.peer, WaitSeconds: 2})
	if err != nil {
		t.Fatal(operation, err)
	}
	return body
}

func TestRemoteAgentDiscoveryDelegationAndDurableResults(t *testing.T) {
	runner := &fakeRunner{text: "Remote analysis result"}
	f := remoteFixtureForTest(t, runner)
	if err := f.b.store.SaveDescriptions(DescriptionUpdate{DescriptionTarget: DescriptionTarget{"project", f.project}, Items: []DescriptionEdit{{ID: "project", Text: "Remote project purpose"}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.b.store.SaveDescriptions(DescriptionUpdate{DescriptionTarget: DescriptionTarget{"agent", "codex"}, Items: []DescriptionEdit{{ID: "agent", Text: "Remote coding agent"}, {ID: "model:test-model", Text: "Model selection guidance"}}}); err != nil {
		t.Fatal(err)
	}
	projects := f.call(t, "projects", federationAgentPayload{})
	if !bytes.Contains(projects, []byte("Remote project purpose")) || bytes.Contains(projects, []byte(`"path"`)) || bytes.Contains(projects, []byte(`"members"`)) {
		t.Fatal("project discovery projection", string(projects))
	}
	agents := f.call(t, "agents", federationAgentPayload{Project: f.project})
	for _, text := range []string{"Remote coding agent", "Model selection guidance", "Fixture coding model", "supportedReasoningEfforts"} {
		if !bytes.Contains(agents, []byte(text)) {
			t.Fatal("missing discovery information", text)
		}
	}
	request := federationAgentPayload{Project: f.project, Backend: chatmodel.BackendCodex, Model: "test-model", Effort: "high", Message: "Inspect project", Context: "Relevant local facts", RequestID: chatmodel.NewID()}
	var task remoteAgentTask
	if err := json.Unmarshal(f.call(t, "delegate", request), &task); err != nil {
		t.Fatal(err)
	}
	if task.Status != "completed" || task.Text != runner.text {
		t.Fatalf("remote result: %+v", task)
	}
	runner.mu.Lock()
	sent := runner.requests[0]
	count := len(runner.requests)
	runner.mu.Unlock()
	if sent.Model != "test-model" || sent.ReasoningEffort != "high" || !strings.Contains(sent.Prompt, "Relevant local facts") || sent.MCP != nil || count != 1 {
		t.Fatalf("delegation context/settings: %#v", sent)
	}
	// Retry even from a later parent turn; this must recover the original task.
	data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "later-turn", remoteAgentOperation{federationAgentPayload: func() federationAgentPayload { request.Operation = "delegate"; return request }(), Server: f.peer})
	if err != nil || !bytes.Contains(data, []byte(task.ID)) {
		t.Fatal("idempotent retry", string(data), err)
	}
	runner.mu.Lock()
	count = len(runner.requests)
	runner.mu.Unlock()
	if count != 1 {
		t.Fatal("retry executed twice")
	}
	request.Message = "Changed input"
	if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{federationAgentPayload: request, Server: f.peer}); err == nil {
		t.Fatal("request ID accepted changed input")
	}
	request.RequestID, request.Conversation = chatmodel.NewID(), task.Conversation
	if err := json.Unmarshal(f.call(t, "delegate", request), &task); err != nil || task.Status != "completed" {
		t.Fatal("follow-up", task, err)
	}
	calls := f.call(t, "calls", federationAgentPayload{})
	if !bytes.Contains(calls, []byte(task.ID)) || !bytes.Contains(calls, []byte(runner.text)) {
		t.Fatal("call receipt not persisted", string(calls))
	}
	f.b.liveMu.Lock()
	f.b.completed = map[string]completedTurn{}
	f.b.liveMu.Unlock()
	if !bytes.Contains(f.call(t, "task", federationAgentPayload{TaskID: task.ID}), []byte(runner.text)) {
		t.Fatal("result depended on live history cache")
	}
	conversations := f.call(t, "conversations", federationAgentPayload{Project: f.project, Limit: 1})
	if !bytes.Contains(conversations, []byte(task.Conversation)) {
		t.Fatal("missing delegated conversation")
	}
	// Native history remains on B and is fetched explicitly in bounded pages.
	path := filepath.Join(t.TempDir(), "remote-native.jsonl")
	appendNativeTurns(t, path, 0, 25, 2)
	if err := f.b.store.SetNativeSessionPath(task.Conversation, "remote-native-session", path); err != nil {
		t.Fatal(err)
	}
	history := f.call(t, "history", federationAgentPayload{Conversation: task.Conversation, Limit: 2})
	var page struct {
		Turns []any  `json:"turns"`
		Next  string `json:"next"`
	}
	if json.Unmarshal(history, &page) != nil || len(page.Turns) != 2 || page.Next == "" || bytes.Contains(history, []byte("function_call_output")) || bytes.Contains(history, []byte(path)) {
		t.Fatal("history page", string(history))
	}
	history = f.call(t, "history", federationAgentPayload{Conversation: task.Conversation, Limit: 2, Before: page.Next})
	if bytes.Contains(history, []byte("answer-024")) {
		t.Fatal("history cursor repeated the newest page")
	}
	var sessions int
	if err := f.b.store.db.QueryRow(`SELECT count(*) FROM auth_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("delegation leaked remote login sessions", sessions, err)
	}
}

func TestRemoteAgentPermissionsAndSourceRevocation(t *testing.T) {
	f := remoteFixtureForTest(t, &fakeRunner{text: "done"})
	f.call(t, "projects", federationAgentPayload{})
	otherProject := testProject(t, f.b.store)
	request := remoteAgentOperation{Server: f.peer, federationAgentPayload: federationAgentPayload{Operation: "delegate", Project: otherProject.ID, Backend: chatmodel.BackendCodex, Message: "unauthorized", RequestID: chatmodel.NewID()}}
	if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request); err == nil {
		t.Fatal("unauthorized project allowed")
	}
	request.Project = f.project
	var task remoteAgentTask
	if err := json.Unmarshal(f.call(t, "delegate", request.federationAgentPayload), &task); err != nil {
		t.Fatal(err)
	}
	otherOrigin, _ := f.a.store.CreateChat(f.identity.User.ID, func() string { p, _ := f.a.store.getSession(f.origin); return p.ProjectID }(), chatmodel.BackendCodex)
	request.Operation, request.TaskID = "task", task.ID
	if _, err := f.a.remoteAgent(t.Context(), f.identity, otherOrigin.ID, "parent-turn", request); err == nil {
		t.Fatal("another parent read remote task")
	}
	var remoteUser string
	if err := f.b.store.db.QueryRow(`SELECT user_id FROM federation_users WHERE subject=?`, f.identity.User.ID).Scan(&remoteUser); err != nil {
		t.Fatal(err)
	}
	p, _ := f.b.store.Projects()
	p[0].Members = nil
	if _, err := f.b.store.SaveProject(p[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request); err == nil {
		t.Fatal("removed project member still fetched result")
	}
	forged := f.identity
	forged.User.SourceServerID = "external"
	if _, err := f.a.remoteAgent(t.Context(), forged, f.origin, "parent-turn", request); err == nil {
		t.Fatal("transitive delegation allowed")
	}
	if err := f.a.store.logout(f.identity.TokenHash); err != nil {
		t.Fatal(err)
	}
	request.Operation = "servers"
	if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request); err == nil {
		t.Fatal("logged-out source retained delegation")
	}
}

func TestRemoteAgentPermissionDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		backend chatmodel.Backend
		mode    string
	}{{chatmodel.BackendCodex, "auto"}, {chatmodel.BackendClaude, "plan"}} {
		t.Run(string(tc.backend), func(t *testing.T) {
			runner := &preferencePermissionRunner{fakeRunner: fakeRunner{text: "done"}}
			f := remoteFixtureForTest(t, runner)
			if err := f.b.store.saveAgent(AgentInfo{Backend: tc.backend, Enabled: true, Installed: true, Catalog: fixtureCatalog()}); err != nil {
				t.Fatal(err)
			}
			if err := f.b.store.saveProjectPermissionMode(f.project, tc.backend, tc.mode); err != nil {
				t.Fatal(err)
			}
			request := federationAgentPayload{Project: f.project, Backend: tc.backend, Message: "Use project permissions", RequestID: chatmodel.NewID()}
			submit := func(request federationAgentPayload) remoteAgentTask {
				t.Helper()
				var task remoteAgentTask
				if err := json.Unmarshal(f.call(t, "delegate", request), &task); err != nil || task.Status != "completed" {
					t.Fatalf("delegation: %+v %v", task, err)
				}
				f.b.turns.Wait()
				return task
			}
			first := submit(request)
			// A continuation must use the project's current choice, even when
			// this conversation last ran with a different mode.
			defaultMode := chatmodel.DefaultPermissionMode(tc.backend)
			if err := f.b.store.saveProjectPermissionMode(f.project, tc.backend, defaultMode); err != nil {
				t.Fatal(err)
			}
			followup := request
			followup.RequestID, followup.Conversation = chatmodel.NewID(), first.Conversation
			submit(followup)
			followup.RequestID, followup.Permission = chatmodel.NewID(), tc.mode
			submit(followup)
			followup.RequestID, followup.Permission = chatmodel.NewID(), ""
			submit(followup)
			runner.mu.Lock()
			var modes []string
			for _, sent := range runner.requests {
				modes = append(modes, sent.PermissionMode)
			}
			runner.mu.Unlock()
			if len(modes) != 4 || modes[0] != tc.mode || modes[1] != defaultMode || modes[2] != tc.mode || modes[3] != defaultMode {
				t.Fatalf("permission modes: got %v, want [%s %s %s %s]", modes, tc.mode, defaultMode, tc.mode, defaultMode)
			}
			if mode, err := f.b.store.projectPermissionMode(f.project, tc.backend); err != nil || mode != defaultMode {
				t.Fatalf("explicit delegation changed the project default: %q %v", mode, err)
			}
			// Recovering an existing task must not rediscover permissions or
			// execute again after the project's mode or policy changes.
			probes := runner.probes
			runner.fail = true
			if replay := submit(request); replay.ID != first.ID || runner.probes != probes {
				t.Fatalf("retry did not recover the original task: %+v", replay)
			}
			runner.mu.Lock()
			defer runner.mu.Unlock()
			if len(runner.requests) != 4 {
				t.Fatal("retry executed again")
			}
		})
	}
}

func TestRemoteAgentRejectsUnavailableDefaultPermission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend chatmodel.Backend
		mode    string
		fail    bool
	}{
		{"codex", chatmodel.BackendCodex, "auto", false},
		{"claude", chatmodel.BackendClaude, "plan", false},
		{"discovery", chatmodel.BackendCodex, "auto", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &preferencePermissionRunner{unavailable: tc.mode, fail: tc.fail}
			f := remoteFixtureForTest(t, runner)
			if err := f.b.store.saveAgent(AgentInfo{Backend: tc.backend, Enabled: true, Installed: true, Catalog: fixtureCatalog()}); err != nil {
				t.Fatal(err)
			}
			if err := f.b.store.saveProjectPermissionMode(f.project, tc.backend, tc.mode); err != nil {
				t.Fatal(err)
			}
			request := federationAgentPayload{Operation: "delegate", Project: f.project, Backend: tc.backend, Message: "Do not use a fallback mode", RequestID: chatmodel.NewID()}
			if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{Server: f.peer, federationAgentPayload: request}); err == nil {
				t.Fatal("unavailable default permission was accepted")
			}
			for _, table := range []string{"sessions", "federation_agent_tasks"} {
				var count int
				if err := f.b.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("rejected request left records in %s: %d %v", table, count, err)
				}
			}
			f.b.turns.Wait()
			runner.mu.Lock()
			count := len(runner.requests)
			runner.mu.Unlock()
			if count != 0 {
				t.Fatal("rejected request invoked the agent")
			}
			if !tc.fail {
				request.Permission = chatmodel.DefaultPermissionMode(tc.backend)
				var task remoteAgentTask
				if err := json.Unmarshal(f.call(t, "delegate", request), &task); err != nil || task.Status != "completed" {
					t.Fatalf("valid explicit mode could not override unavailable default: %+v %v", task, err)
				}
			}
		})
	}
}

func TestRemoteAgentNativeHistoryPagination(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		count, limit, turnBytes, growAfter int
	}{
		{"multiple_windows", 197, 8, 10, 1},
		{"byte_limited_windows", 35, 3, 60_000, 1},
		{"older_page", 197, 8, 10, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := remoteFixtureForTest(t, &fakeRunner{text: "done"})
			var task remoteAgentTask
			if err := json.Unmarshal(f.call(t, "delegate", federationAgentPayload{Project: f.project, Backend: chatmodel.BackendCodex, Message: "Work", RequestID: chatmodel.NewID()}), &task); err != nil {
				t.Fatal(err)
			}
			f.b.turns.Wait()
			// Simulate the absence of process-local completion state after a
			// restart. The native history and cursors must stand on their own.
			f.b.liveMu.Lock()
			delete(f.b.completed, task.Conversation)
			f.b.liveMu.Unlock()
			f.b.store.runtimeMu.Lock()
			delete(f.b.store.runtimeTurns, task.Conversation)
			f.b.store.runtimeMu.Unlock()
			path := filepath.Join(t.TempDir(), "remote-native.jsonl")
			appendNativeTurns(t, path, 0, tc.count, 0)
			const thread = "remote-native-history"
			if err := f.b.store.SetNativeSessionPath(task.Conversation, thread, path); err != nil {
				t.Fatal(err)
			}
			reader := &fakeCodexHistory{count: tc.count, turnBytes: tc.turnBytes, idPrefix: "native-"}
			f.b.store.codexHistory.reader = reader
			before, seen, pages, appended := "", 0, 0, false
			for {
				var page struct {
					Turns []struct {
						Messages []struct{ Role, Text string } `json:"messages"`
					} `json:"turns"`
					Next string `json:"next"`
				}
				if err := json.Unmarshal(f.call(t, "history", federationAgentPayload{Conversation: task.Conversation, Before: before, Limit: tc.limit}), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Turns) == 0 || len(page.Turns) > tc.limit || seen+len(page.Turns) > tc.count {
					t.Fatalf("invalid page size %d after %d turns", len(page.Turns), seen)
				}
				for i, turn := range page.Turns {
					want := fmt.Sprintf("answer-native-turn-%03d", tc.count-seen-len(page.Turns)+i)
					if len(turn.Messages) != 2 || turn.Messages[1].Role != "assistant" || turn.Messages[1].Text != want {
						t.Fatalf("history skipped or repeated a turn: got %+v, want %s", turn.Messages, want)
					}
				}
				seen += len(page.Turns)
				pages++
				if page.Next == "" {
					break
				}
				if !appended && pages == tc.growAfter {
					// Every cursor, including the very first page's cursor, must
					// survive the anchor leaving the newest cached window.
					appendNativeTurns(t, path, tc.count, 100, 0)
					reader.count += 100
					f.b.store.codexHistory.forget(thread)
					appended = true
				}
				before = page.Next
			}
			if seen != tc.count || !appended {
				t.Fatalf("history pagination incomplete: read %d of %d, grew history=%v", seen, tc.count, appended)
			}
		})
	}
}

func TestRemoteAgentNativeCommandsUseTheRemoteSession(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			fixture := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: fixture.Binary})
			f := remoteFixtureForTest(t, runner)
			project, err := f.b.store.Project(f.project)
			if err != nil {
				t.Fatal(err)
			}
			project.Path = fixture.WorkingDirectory
			if _, err := f.b.store.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			if err := f.b.store.saveAgent(AgentInfo{Backend: backend, Enabled: true, Installed: true, Executable: fixture.Binary, Catalog: fixtureCatalog()}); err != nil {
				t.Fatal(err)
			}
			conversation := ""
			for _, command := range []struct{ message, status, output string }{
				{"/review remote-changes", "completed", "Hello world"},
				{"/goal inspect remote changes", "completed", "Hello world"},
				{"/goal", "completed", "inspect remote changes"},
				{" /goal clear", "completed", "Goal cleared."},
				{"/goal", "completed", "No goal set."},
				{"/plan investigate", "completed", "Hello world"},
				{"/unknown-command task", "failed", ""},
			} {
				var task remoteAgentTask
				if err := json.Unmarshal(f.call(t, "delegate", federationAgentPayload{Project: f.project, Backend: backend, Message: command.message, Conversation: conversation, RequestID: chatmodel.NewID()}), &task); err != nil {
					t.Fatal(err)
				}
				if task.Status != command.status || command.output != "" && !strings.Contains(task.Text, command.output) || command.status == "failed" && task.Error == "" {
					t.Fatalf("remote command %q: %+v", command.message, task)
				}
				if conversation != "" && task.Conversation != conversation {
					t.Fatal("native command switched remote conversations")
				}
				conversation = task.Conversation
				f.b.turns.Wait()
			}
			var review, goalSet, goalGet, goalClear bool
			for _, entry := range fixtureLog(t, fixture) {
				value, _ := entry["value"].(map[string]any)
				method, _ := entry["method"].(string)
				if backend == chatmodel.BackendCodex {
					review = review || method == "review/start" && agentfixture.NestedString(value, "params", "target", "branch") == "remote-changes"
					goalSet = goalSet || method == "thread/goal/set" && agentfixture.NestedString(value, "params", "objective") == "inspect remote changes"
					goalGet = goalGet || method == "thread/goal/get"
					goalClear = goalClear || method == "thread/goal/clear"
					if method != "turn/start" {
						continue
					}
					params, _ := value["params"].(map[string]any)
					inputs, _ := params["input"].([]any)
					for _, input := range inputs {
						block, _ := input.(map[string]any)
						if text := agentfixture.StringValue(block["text"]); chatmodel.ParseSlashCommand(text) != nil {
							t.Fatalf("native command fell back to a plain prompt: %q", text)
						}
					}
				} else if backend == chatmodel.BackendClaude && method == "user" {
					prompt := agentfixture.NestedString(value, "message", "content")
					review = review || prompt == "/review remote-changes"
					goalSet = goalSet || prompt == "/goal inspect remote changes"
					goalGet = goalGet || prompt == "/goal"
					goalClear = goalClear || prompt == "/goal clear"
					if strings.Contains(prompt, "unknown-command") {
						t.Fatal("unsupported command was sent as a prompt")
					}
				}
			}
			if !review || !goalSet || !goalGet || !goalClear {
				t.Fatalf("native command routes: review=%v goal set=%v get=%v clear=%v", review, goalSet, goalGet, goalClear)
			}
		})
	}
}

func TestRemoteAgentCancellationAndRestartRecovery(t *testing.T) {
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	f := remoteFixtureForTest(t, runner)
	request := remoteAgentOperation{Server: f.peer, federationAgentPayload: federationAgentPayload{Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Message: "Wait", RequestID: chatmodel.NewID()}}
	data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", request)
	if err != nil {
		t.Fatal(err)
	}
	var task remoteAgentTask
	_ = json.Unmarshal(data, &task)
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("remote task did not start")
	}
	f.call(t, "cancel", federationAgentPayload{TaskID: task.ID})
	data = f.call(t, "task", federationAgentPayload{TaskID: task.ID})
	if !bytes.Contains(data, []byte(`"status":"stopped"`)) {
		t.Fatal("remote cancellation", string(data))
	}
	if _, err := f.b.store.db.Exec(`UPDATE federation_agent_tasks SET state='running',body=json_set(body,'$.status','running') WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	data = f.call(t, "task", federationAgentPayload{TaskID: task.ID})
	if !bytes.Contains(data, []byte(`"status":"interrupted"`)) {
		t.Fatal("orphaned task remained running", string(data))
	}
}

func TestRemoteAgentUserRoutingAndOldTaskCannotCancelFollowup(t *testing.T) {
	runner := &fakeRunner{text: "Direct user result"}
	f := remoteFixtureForTest(t, runner)
	cookie, csrf := loginRequest(t, f.a, f.identity.User.Username)
	input := remoteAgentOperation{Server: f.peer, WaitSeconds: 2, federationAgentPayload: federationAgentPayload{Operation: "delegate", OriginChat: f.origin, Project: f.project, Backend: chatmodel.BackendCodex, Message: "Actual message body", RequestID: chatmodel.NewID()}}
	body, _ := json.Marshal(input)
	response := apiRequest(f.a, "POST", "/api/federation/agents", string(body), cookie, csrf)
	var old remoteAgentTask
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &old) != nil || old.Status != "completed" || old.TurnID == "" {
		t.Fatal("direct user routing failed", response.Code, response.Body.String())
	}
	runner.mu.Lock()
	if len(runner.requests) != 1 || runner.requests[0].Prompt != input.Message || runner.requests[0].MCP != nil {
		t.Fatal("direct call changed the body or started MCP", runner.requests)
	}
	runner.mu.Unlock()
	if calls := f.call(t, "calls", federationAgentPayload{}); !bytes.Contains(calls, []byte(input.Message)) {
		t.Fatal("user message not retained in receipt", string(calls))
	}
	f.b.turns.Wait()
	f.a.runner.(*fakeRunner).mu.Lock()
	localRuns := len(f.a.runner.(*fakeRunner).requests)
	f.a.runner.(*fakeRunner).mu.Unlock()
	if localRuns != 0 {
		t.Fatal("direct remote send started a local agent")
	}
	// Simulate an old receipt whose completion write was lost before restart.
	if _, err := f.b.store.db.Exec(`UPDATE federation_agent_tasks SET state='running',body=json_set(body,'$.status','running') WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	gate := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	f.b.runner = gate
	input.Conversation, input.RequestID = old.Conversation, chatmodel.NewID()
	input.WaitSeconds = 0
	data, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "later-turn", input)
	var current remoteAgentTask
	if err != nil || json.Unmarshal(data, &current) != nil || current.TurnID == old.TurnID {
		t.Fatal("followup did not own a new turn", string(data), err)
	}
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("followup did not start")
	}
	if data := f.call(t, "cancel", federationAgentPayload{TaskID: old.ID}); !bytes.Contains(data, []byte(`"status":"interrupted"`)) {
		t.Fatal("orphaned old task not marked interrupted", string(data))
	}
	data, err = f.a.remoteAgent(t.Context(), f.identity, f.origin, "later-turn", remoteAgentOperation{Server: f.peer, federationAgentPayload: federationAgentPayload{Operation: "task", TaskID: current.ID}})
	if err != nil || !bytes.Contains(data, []byte(`"status":"running"`)) {
		t.Fatal("old task cancellation stopped its followup", string(data), err)
	}
	f.call(t, "cancel", federationAgentPayload{TaskID: current.ID})
}

func TestAgentMCPToolsCapabilitiesAndLifecycle(t *testing.T) {
	f := remoteFixtureForTest(t, &fakeRunner{text: "MCP result"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	config, release, err := f.a.agentMCPGrant(ctx, f.identity, f.origin, "parent-turn")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	call := func(token, method string, args any) *http.Response {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": args})
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, config.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if response := call("invalid", "tools/list", nil); response.StatusCode != 401 {
		t.Fatal("MCP accepted invalid capability")
	}
	response := call(config.Token, "tools/list", nil)
	var listed struct {
		Result struct {
			Tools []mcpTool `json:"tools"`
		} `json:"result"`
	}
	if json.NewDecoder(response.Body).Decode(&listed) != nil || len(listed.Result.Tools) != 10 {
		t.Fatal("incomplete MCP tool catalog")
	}
	for _, input := range []map[string]any{
		{"name": "spool_approve", "arguments": map[string]any{}},
		{"name": "spool_projects", "arguments": map[string]any{"server": f.peer, "origin_chat": "forged-parent"}},
		{"name": "spool_delegate", "arguments": map[string]any{"server": f.peer, "approval": map[string]any{"allow": true}}},
	} {
		response = call(config.Token, "tools/call", input)
		var refused map[string]any
		if json.NewDecoder(response.Body).Decode(&refused) != nil || refused["error"] == nil {
			t.Fatal("MCP accepted approval or scope injection", refused)
		}
	}
	response = call(config.Token, "tools/call", map[string]any{"name": "spool_projects", "arguments": map[string]any{"server": f.peer}})
	var result map[string]any
	_ = json.NewDecoder(response.Body).Decode(&result)
	encoded, _ := json.Marshal(result)
	if !bytes.Contains(encoded, []byte(f.project)) {
		t.Fatal("MCP did not return authorized projects", string(encoded))
	}
	response = call(config.Token, "tools/call", map[string]any{"name": "spool_delegate", "arguments": map[string]any{"server": f.peer, "project": f.project, "agent": "codex", "message": "Do work", "request_id": chatmodel.NewID(), "wait_seconds": 2}})
	_ = json.NewDecoder(response.Body).Decode(&result)
	encoded, _ = json.Marshal(result)
	if !bytes.Contains(encoded, []byte("MCP result")) {
		t.Fatal("MCP result not available as context", string(encoded))
	}
	release()
	if response := call(config.Token, "tools/list", nil); response.StatusCode != 401 {
		t.Fatal("completed turn retained MCP capability")
	}
}

func TestRemoteAgentReadOnlyProjectsRejectUserAndMCPDelegation(t *testing.T) {
	runner := &fakeRunner{text: "Existing result remains readable"}
	f := remoteFixtureForTest(t, runner)
	request := federationAgentPayload{Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Message: "Initial task", RequestID: chatmodel.NewID()}
	var task remoteAgentTask
	if err := json.Unmarshal(f.call(t, "delegate", request), &task); err != nil || task.Status != "completed" {
		t.Fatal("initial delegation", task, err)
	}
	path := filepath.Join(t.TempDir(), "remote-native.jsonl")
	appendNativeTurns(t, path, 0, 1, 2)
	if err := f.b.store.SetNativeSessionPath(task.Conversation, "remote-native-session", path); err != nil {
		t.Fatal(err)
	}
	f.b.store.codexHistory.reader = &fakeCodexHistory{count: 1}
	var remoteUser string
	if err := f.b.store.db.QueryRow(`SELECT user_id FROM federation_users WHERE subject=?`, f.identity.User.ID).Scan(&remoteUser); err != nil {
		t.Fatal(err)
	}
	projects, err := f.b.store.Projects()
	if err != nil || len(projects) != 1 || projects[0].ID != f.project {
		t.Fatal("remote project", projects, err)
	}
	project := projects[0]
	config, release, err := f.a.agentMCPGrant(t.Context(), f.identity, f.origin, "parent-turn")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	type mcpResult struct {
		IsError bool            `json:"isError"`
		Data    json.RawMessage `json:"structuredContent"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	mcpCall := func(t *testing.T, name string, arguments any) mcpResult {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+config.Token)
		response := httptest.NewRecorder()
		f.a.serveAgentMCP(response, req)
		var message struct {
			Result mcpResult       `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &message) != nil || len(message.Error) != 0 {
			t.Fatal("MCP response", response.Code, response.Body.String())
		}
		return message.Result
	}
	cookie, csrf := loginRequest(t, f.a, f.identity.User.Username)
	for _, sharedAccess := range []string{"control", "read-only"} {
		t.Run(sharedAccess, func(t *testing.T) {
			// Shared-chat control must not override read-only personal/project access.
			project.MemberAccess[remoteUser], project.MemberOwnAccess[remoteUser] = sharedAccess, "read-only"
			if _, err := f.b.store.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			discovery := mcpCall(t, "spool_projects", map[string]any{"server": f.peer})
			var listed struct {
				Projects []Project `json:"result"`
			}
			if discovery.IsError || json.Unmarshal(discovery.Data, &listed) != nil || len(listed.Projects) != 1 || listed.Projects[0].ID != f.project || listed.Projects[0].CanCreate == nil || *listed.Projects[0].CanCreate {
				t.Fatal("MCP must advertise read-only projects as ineligible", string(discovery.Data))
			}
			for _, mode := range []string{"new", "continue", "retry"} {
				t.Run(mode, func(t *testing.T) {
					input := request
					if mode != "retry" {
						input.RequestID = chatmodel.NewID()
					}
					if mode == "continue" {
						input.Conversation = task.Conversation
					}
					body, _ := json.Marshal(remoteAgentOperation{Server: f.peer, federationAgentPayload: input})
					var arguments map[string]any
					_ = json.Unmarshal(body, &arguments)
					delete(arguments, "operation")
					if result := mcpCall(t, "spool_delegate", arguments); !result.IsError || len(result.Content) != 1 || result.Content[0].Text != "Project does not allow creating agent conversations" {
						t.Fatal("MCP must reject read-only delegation", result)
					}
					input.OriginChat = f.origin
					body, _ = json.Marshal(remoteAgentOperation{Server: f.peer, federationAgentPayload: input})
					response := apiRequest(f.a, http.MethodPost, "/api/federation/agents", string(body), cookie, csrf)
					if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Project does not allow creating agent conversations") {
						t.Fatal("user API must reject read-only delegation", response.Code, response.Body.String())
					}
				})
			}
			if result := f.call(t, "task", federationAgentPayload{TaskID: task.ID}); !bytes.Contains(result, []byte(runner.text)) {
				t.Fatal("read-only access lost the existing result", string(result))
			}
			if history := f.call(t, "history", federationAgentPayload{Conversation: task.Conversation}); !bytes.Contains(history, []byte("answer-turn-000")) {
				t.Fatal("read-only access lost existing history", string(history))
			}
		})
	}
	for _, table := range []string{"sessions", "federation_agent_tasks"} {
		var count int
		if err := f.b.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatal("read-only delegation created records", table, count, err)
		}
	}
	runner.mu.Lock()
	count := len(runner.requests)
	runner.mu.Unlock()
	if count != 1 {
		t.Fatal("read-only delegation started an agent", count)
	}
}

func TestRemoteAgentHumanApprovalRequiresCurrentPermission(t *testing.T) {
	runner := &elicitationRunner{response: make(chan chatmodel.ApprovalResponse, 1)}
	f := remoteFixtureForTest(t, runner)
	var task remoteAgentTask
	if err := json.Unmarshal(f.call(t, "delegate", federationAgentPayload{Project: f.project, Backend: chatmodel.BackendCodex, Message: "Ask the user", RequestID: chatmodel.NewID()}), &task); err != nil || task.Status != "awaiting_approval" || task.Approval == nil || task.TurnID == "" {
		t.Fatal("remote approval not surfaced", task, err)
	}
	var remoteUser string
	if err := f.b.store.db.QueryRow(`SELECT user_id FROM federation_users WHERE subject=?`, f.identity.User.ID).Scan(&remoteUser); err != nil {
		t.Fatal(err)
	}
	projects, _ := f.b.store.Projects()
	project := projects[0]
	project.MemberAccess[remoteUser], project.MemberOwnAccess[remoteUser] = "read-only", "read-only"
	if _, err := f.b.store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	approval := &approvalInput{ID: task.Approval.ID, TurnID: task.TurnID, Allow: true, Content: map[string]any{"confirm": true}}
	for _, op := range []string{"approve", "cancel", "delegate"} {
		request := federationAgentPayload{Operation: op, TaskID: task.ID, Project: f.project, Backend: chatmodel.BackendCodex, Message: "Read-only", RequestID: chatmodel.NewID()}
		if op == "approve" {
			request.Approval = approval
		}
		if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{Server: f.peer, federationAgentPayload: request}); err == nil {
			t.Fatal("read-only remote user could", op)
		}
	}
	f.call(t, "task", federationAgentPayload{TaskID: task.ID})
	project.MemberAccess[remoteUser], project.MemberOwnAccess[remoteUser] = "control", "create"
	if _, err := f.b.store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginRequest(t, f.a, f.identity.User.Username)
	body, _ := json.Marshal(remoteAgentOperation{Server: f.peer, federationAgentPayload: federationAgentPayload{Operation: "approve", OriginChat: f.origin, TaskID: task.ID, Approval: approval}})
	for _, auth := range []struct {
		cookie *http.Cookie
		csrf   string
		status int
	}{{nil, "", 401}, {cookie, "", 403}, {cookie, csrf, 200}} {
		response := apiRequest(f.a, "POST", "/api/federation/agents", string(body), auth.cookie, auth.csrf)
		if response.Code != auth.status {
			t.Fatal("remote approval API auth", response.Code, response.Body.String())
		}
	}
	select {
	case response := <-runner.response:
		if !response.Allow || response.Content["confirm"] != true {
			t.Fatal("human response lost", response)
		}
	case <-time.After(time.Second):
		t.Fatal("remote user approval did not resume agent")
	}
	result := f.call(t, "task", federationAgentPayload{TaskID: task.ID})
	if !bytes.Contains(result, []byte(`"status":"completed"`)) {
		t.Fatal("approved remote task did not complete", string(result))
	}
	var actor string
	if err := f.b.store.db.QueryRow(`SELECT user_id FROM turn_actions WHERE approval_id=?`, approval.ID).Scan(&actor); err != nil || actor != remoteUser {
		t.Fatal("remote approval actor not audited", actor, err)
	}
}

func TestRemoteAgentValidationReplayAndDeletion(t *testing.T) {
	f := remoteFixtureForTest(t, &preferencePermissionRunner{fakeRunner: fakeRunner{text: "durable result"}, unavailable: "auto"})
	f.call(t, "projects", federationAgentPayload{})
	request := federationAgentPayload{Operation: "delegate", Project: f.project, Backend: chatmodel.BackendCodex, Model: "test-model", Message: "Do work", RequestID: chatmodel.NewID()}
	for _, invalid := range []federationAgentPayload{
		func() federationAgentPayload {
			r := request
			r.Message, r.Context = "/goal clear", "reference"
			return r
		}(),
		func() federationAgentPayload {
			r := request
			r.Message = strings.Repeat("x", chatmodel.MaxMessageBytes)
			r.Context = "overflow"
			return r
		}(),
		func() federationAgentPayload { r := request; r.Permission = "auto"; return r }(),
	} {
		if _, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "parent-turn", remoteAgentOperation{Server: f.peer, federationAgentPayload: invalid}); err == nil {
			t.Fatal("invalid remote request accepted")
		}
	}
	var count int
	if err := f.b.store.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("validation left an orphan conversation", count, err)
	}
	var task remoteAgentTask
	_ = json.Unmarshal(f.call(t, "delegate", request), &task)
	if err := f.b.store.ConfigureAgent(chatmodel.BackendCodex, false, ""); err != nil {
		t.Fatal(err)
	}
	if result := f.call(t, "delegate", request); !bytes.Contains(result, []byte(task.ID)) || !bytes.Contains(result, []byte("durable result")) {
		t.Fatal("disabled model prevented recovering an existing task", string(result))
	}
	chat, _ := f.b.store.getSession(task.Conversation)
	if err := f.b.store.removeDeletedSession(chat); err != nil {
		t.Fatal("delegation prevented remote conversation deletion", err)
	}
	if err := f.b.store.db.QueryRow(`SELECT count(*) FROM federation_agent_tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted conversation retained tasks", count, err)
	}
	local, _ := f.a.store.getSession(f.origin)
	if err := f.a.store.removeDeletedSession(local); err != nil {
		t.Fatal(err)
	}
	if err := f.a.store.db.QueryRow(`SELECT count(*) FROM federation_agent_calls`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted parent retained receipts", count, err)
	}
}

func TestAgentMCPAutomaticallyUsesTheTurnActorAndExpires(t *testing.T) {
	f := remoteFixtureForTest(t, &fakeRunner{text: "remote"})
	runner := f.a.runner.(*fakeRunner)
	send := func() agent.AgentRequest {
		t.Helper()
		chat, _ := f.a.store.Get(f.origin)
		if err := f.a.startTurnWithInput(chat, messageInput{userID: f.identity.User.ID, agentIdentity: &f.identity, Message: "Use remote tools if needed", MCPServers: []string{"spool_federation"}}, chatmodel.CodexOptions{}); err != nil {
			t.Fatal(err)
		}
		f.a.turns.Wait()
		runner.mu.Lock()
		defer runner.mu.Unlock()
		request := runner.requests[len(runner.requests)-1]
		if request.MCP == nil {
			t.Fatal("normal authenticated turn did not receive MCP")
		}
		return request
	}
	first := send()
	list := func() int {
		t.Helper()
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, first.MCP.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		request.Header.Set("Authorization", "Bearer "+first.MCP.Token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if list() != http.StatusUnauthorized {
		t.Fatal("completed turn retained its MCP capability")
	}
	// The persistent CLI keeps its configuration: a new token would restart it.
	if second := send(); *second.MCP != *first.MCP {
		t.Fatal("following turn of the same user changed the MCP capability")
	}
	if list() != http.StatusUnauthorized {
		t.Fatal("capability stayed usable between turns")
	}
}
