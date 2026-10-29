package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"spool/internal/agent"
	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

type issueTestRunner struct {
	fakeRunner
	work func(context.Context, agent.AgentRequest) (agent.AgentResult, error)
}

func (r *issueTestRunner) Run(ctx context.Context, request agent.AgentRequest, _ func(agent.AgentEvent) error) (agent.AgentResult, error) {
	return r.work(ctx, request)
}

type issueCommands struct {
	mu       sync.Mutex
	root     string
	openPR   bool
	noLabels bool
	fail     string
	calls    []string
}

func (f *issueCommands) command(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := strings.Join(args, " ")
	f.calls = append(f.calls, name+" "+call)
	if name == "git" {
		switch {
		case call == "rev-parse --show-toplevel":
			return []byte(f.root), nil
		case call == "remote get-url origin":
			return []byte("git@github.com:fixture/issues.git"), nil
		case call == "worktree list --porcelain":
			return []byte("worktree " + f.root + "\nHEAD abc\nbranch refs/heads/dev\n"), nil
		case call == "symbolic-ref --short -q HEAD":
			return []byte("dev"), nil
		case strings.HasPrefix(call, "status "):
			return []byte(" M user-file.txt"), nil
		default:
			return nil, fmt.Errorf("unexpected modifying git call %s", call)
		}
	}
	if name != "gh" {
		return nil, fmt.Errorf("unexpected program %s", name)
	}
	if f.fail != "" {
		return nil, errors.New(f.fail)
	}
	switch {
	case strings.Contains(call, "api graphql"):
		return []byte(`[{"data":{"repository":{"issue":{"timelineItems":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}]`), nil
	case strings.HasPrefix(call, "pr view"):
		return []byte(`{"url":"https://github.com/fixture/issues/pull/20","state":"OPEN","isDraft":true,"headRefName":"codex/issue-1-fix","baseRefName":"dev"}`), nil
	case strings.Contains(call, "/issues?state=all"):
		labels := `[{"name":"web"},{"name":"server"}]`
		if f.noLabels {
			labels = `[{"name":"Web"}]`
		}
		return []byte(`[[{"number":1,"title":"Fix fixture","state":"open","html_url":"https://github.com/fixture/issues/issues/1","body":"Reproduce this","labels":` + labels + `}]]`), nil
	case strings.Contains(call, "/pulls?"):
		if f.openPR {
			return []byte(`[[{"html_url":"https://github.com/fixture/issues/pull/20","head":{"ref":"codex/issue-1-fix"}}]]`), nil
		}
		return []byte(`[[]]`), nil
	case strings.Contains(call, "/timeline?"):
		return []byte(`[[]]`), nil
	case strings.Contains(call, "/comments?"):
		return []byte(`[[{"body":"An extra requirement"}]]`), nil
	default:
		return nil, fmt.Errorf("unexpected GH command %s", call)
	}
}

func issueFixture(t *testing.T) (*App, Project, *issueCommands, *issueTestRunner) {
	t.Helper()
	s := testStore(t)
	u := testUser(t, s, "automation")
	p := testProject(t, s, u)
	testAgent(t, s)
	for _, args := range [][]string{{"init", "-b", "dev"}, {"remote", "add", "origin", "git@github.com:fixture/issues.git"}} {
		if _, err := issues.Run(context.Background(), p.Path, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	p.Issues = IssuesAutomation{Enabled: true, IntervalMinutes: 5, Labels: []string{"web", "server", "app"}, UserID: u.ID, Backend: chatmodel.BackendCodex, Model: "test-model", Permission: "auto"}
	var err error
	p, err = s.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	runner := &issueTestRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	f := &issueCommands{root: p.Path}
	a.automation = &issueScheduler{instance: "test-instance", run: f.command, active: map[string]bool{}}
	return a, p, f, runner
}

func TestIssuesSettingsPersistValidateAndDoNotDelayUnchangedSchedules(t *testing.T) {
	a, p, _, _ := issueFixture(t)
	v, err := a.store.IssuesAutomation(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || v.IntervalMinutes != 5 || v.Repository != "fixture/issues" || v.Backend != chatmodel.BackendCodex || v.NextRunAt == 0 {
		t.Fatalf("configuration lost: %#v", v)
	}
	if _, err = a.store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := a.store.IssuesAutomation(p.ID)
	if unchanged.NextRunAt != v.NextRunAt {
		t.Fatal("saving unchanged config delayed the schedule")
	}
	for _, mutate := range []func(*IssuesAutomation){func(v *IssuesAutomation) { v.IntervalMinutes = 0 }, func(v *IssuesAutomation) { v.UserID = "outsider" }, func(v *IssuesAutomation) { v.Backend = "other" }, func(v *IssuesAutomation) { v.Permission = "bypass" }} {
		bad := p
		mutate(&bad.Issues)
		if _, err = a.store.SaveProject(bad); err == nil {
			t.Fatal("invalid configuration persisted")
		}
	}
	if _, err = a.store.SaveProject(Project{ID: p.ID, Name: p.Name, Path: p.Path, Members: p.Members}); err != nil {
		t.Fatal(err)
	}
}

func TestIssuesSettingsDeriveRepositoryInsteadOfTrustingInput(t *testing.T) {
	a, p, _, _ := issueFixture(t)
	for _, repository := range []string{"", "another/repository", "https://github.com/a/b"} {
		p.Issues.Repository = repository
		saved, err := a.store.SaveProject(p)
		if err != nil || saved.Issues.Repository != "fixture/issues" {
			t.Fatalf("input %q: saved repository = %q, error = %v", repository, saved.Issues.Repository, err)
		}
		stored, err := a.store.IssuesAutomation(p.ID)
		if err != nil || stored.Repository != "fixture/issues" {
			t.Fatalf("derived repository not persisted: %#v, %v", stored, err)
		}
	}
}

func TestIssuesSettingsRequireProjectGitAndGitHubOrigin(t *testing.T) {
	a, p, _, _ := issueFixture(t)
	for _, state := range []string{"no git", "no origin", "non-GitHub origin", "subdirectory"} {
		t.Run(state, func(t *testing.T) {
			bad := p
			bad.ID, bad.Path = "", t.TempDir()
			git := func(args ...string) {
				t.Helper()
				if _, err := issues.Run(context.Background(), bad.Path, "git", args...); err != nil {
					t.Fatal(err)
				}
			}
			if state != "no git" {
				git("init", "-b", "dev")
			}
			switch state {
			case "non-GitHub origin":
				git("remote", "add", "origin", "git@gitlab.com:fixture/issues.git")
			case "subdirectory":
				git("remote", "add", "origin", "https://github.com/fixture/issues.git")
				bad.Path = filepath.Join(bad.Path, "subdirectory")
				if err := os.Mkdir(bad.Path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			// Even a manually supplied, well-formed owner/repo cannot bypass Git.
			if _, err := a.store.SaveProject(bad); err == nil {
				t.Fatal("enabled automation without a project GitHub origin")
			}
			bad.Issues.Enabled = false
			if _, err := a.store.SaveProject(bad); err != nil {
				t.Fatalf("ordinary projects must not require Git: %v", err)
			}
		})
	}
	if _, err := issues.Run(context.Background(), p.Path, "git", "remote", "remove", "origin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.SaveProject(p); err == nil {
		t.Fatal("saved enabled automation after origin was removed")
	}
	p.Issues.Enabled = false
	if _, err := a.store.SaveProject(p); err != nil {
		t.Fatalf("missing origin prevented disabling an existing schedule: %v", err)
	}
}

func TestIssueRoundFailureRecoveryUsesNewChatsAndSameSharedClaim(t *testing.T) {
	a, p, f, runner := issueFixture(t)
	if _, err := a.store.db.Exec(`CREATE TRIGGER reject_issue_artifacts BEFORE INSERT ON chat_artifacts BEGIN SELECT RAISE(ABORT,'issue automation must not write artifacts'); END`); err != nil {
		t.Fatal(err)
	}
	var requests []agent.AgentRequest
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		requests = append(requests, request)
		directory := issues.Directory(a.store.Root(), p.Path)
		if len(request.WritableDirectories) != 1 || !strings.HasPrefix(request.WritableDirectories[0], filepath.Join(directory, "runs")+string(os.PathSeparator)) {
			t.Fatalf("agent must only get its project's issues directory: %v", request.WritableDirectories)
		}
		if !strings.Contains(request.Prompt, filepath.Join(issues.Directory(a.store.Root(), p.Path), "issues.json")) || !strings.Contains(request.Prompt, "records 数组") || !strings.Contains(request.Prompt, "notes、validation 是字符串") || strings.Contains(request.Prompt, "spool:issues:v1") {
			t.Fatal("agent did not receive the standalone JSON ledger contract")
		}
		ledger, err := issues.Load(request.WritableDirectories[0])
		if err != nil {
			t.Fatal(err)
		}
		r := ledger.Records[1]
		if r.Status != "processing" || r.Owner.Automation != "issues" || r.Owner.Chat != request.ChatID || r.Branch != "codex/issue-1-fix" || r.Worktree == "" {
			t.Fatalf("editing began before a complete claim: %#v", r)
		}
		if _, err = os.Stat(filepath.Join(request.WritableDirectories[0], ".issues-automation.lock", "owner.json")); err != nil {
			t.Fatal("project lock not held during agent execution")
		}
		if len(requests) == 1 {
			r.Notes = "tests fail; preserve files and resume this worktree"
			r.Validation = "go test ./... FAILED"
			if err = ledger.Save(); err != nil {
				t.Fatal(err)
			}
			return agent.AgentResult{}, errors.New("verification failed")
		}
		if !strings.Contains(request.Prompt, `"resume": true`) || !strings.Contains(request.Prompt, "tests fail; preserve files") {
			t.Fatal("new chat did not recover shared progress")
		}
		r.Validation = "go test ./..., lint, build and git diff --check passed"
		r.Notes = "implemented and verified"
		if err = ledger.Save(); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.openPR = true
		f.mu.Unlock()
		return agent.AgentResult{Text: "已创建 PR"}, nil
	}
	if err := a.runIssueRound(context.Background(), p); err == nil {
		t.Fatal("failed verification treated as successful")
	}
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil {
		t.Fatal(err)
	}
	first := ledger.Records[1].Owner
	if ledger.Records[1].Status != "processing" {
		t.Fatal("failure released processing claim")
	}
	if _, err = os.Stat(filepath.Join(issues.Directory(a.store.Root(), p.Path), ".issues-automation.lock")); !os.IsNotExist(err) {
		t.Fatal("lock not safely released")
	}
	if err = a.runIssueRound(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ledger, _ = issues.Load(issues.Directory(a.store.Root(), p.Path))
	record := ledger.Records[1]
	if record.Status != "pr" || record.Owner.Run == first.Run || record.Owner.Chat == first.Chat || len(record.PRs) != 1 {
		t.Fatalf("recovery state incorrect: %#v", record)
	}
	if len(requests) != 2 || requests[0].ChatID == requests[1].ChatID || requests[0].NativeSessionID != "" || requests[1].NativeSessionID != "" || requests[1].PermissionMode != "auto" {
		t.Fatal("reused a previous native chat or lost configured agent options")
	}
	chat, ok := a.store.Get(record.Owner.Chat)
	if !ok || len(chat.Artifacts) != 0 {
		t.Fatal("issue automation wrote PR attachments instead of using the JSON ledger")
	}
	if len(chat.Turns) != 1 || chat.Turns[0].UserID != chatmodel.SpoolBotID || len(chat.Participants) != 1 || chat.Participants[0].ID != chatmodel.SpoolBotID {
		t.Fatalf("scheduled issue task must be attributed to Spool Bot: %+v", chat)
	}
	if !strings.Contains(requests[1].Prompt, "git pull --ff-only origin dev") || strings.Contains(requests[1].Prompt, "attach_artifact") || strings.Contains(requests[1].Prompt, "Authorization: Bearer") || !strings.Contains(requests[1].Prompt, "prs 数组") || !strings.Contains(requests[1].Prompt, "沙箱外联网") {
		t.Fatal("agent execution contract incomplete")
	}
	if err = a.runIssueRound(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatal("opened another task for an issue with an existing draft PR")
	}
	if _, err := os.Stat(filepath.Join(p.Path, "docs")); !os.IsNotExist(err) {
		t.Fatal("automation created state inside the source checkout")
	}
}

func TestIssueRoundUsesDetectedRepositoryWhenStoredRepositoryIsEmpty(t *testing.T) {
	a, p, _, runner := issueFixture(t)
	p.Issues.Repository = ""
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		if !strings.Contains(request.Prompt, `"repository": "fixture/issues"`) {
			t.Error("agent prompt did not use the project Git origin")
		}
		return agent.AgentResult{}, errors.New("verification unavailable")
	}
	if err := a.runIssueRound(context.Background(), p); err == nil || !strings.Contains(err.Error(), "verification unavailable") {
		t.Fatalf("round did not reach the agent with the detected repository: %v", err)
	}
}

func TestIssueRoundRejectsMissingOrChangedGitBeforeGitHubAccess(t *testing.T) {
	for _, remote := range []string{"", "https://github.com/another/repository.git", "git@gitlab.com:fixture/issues.git"} {
		t.Run(remote, func(t *testing.T) {
			a, p, commands, runner := issueFixture(t)
			runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
				t.Error("agent started without matching project Git information")
				return agent.AgentResult{}, nil
			}
			a.automation.run = func(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
				if name == "gh" {
					t.Error("queried GitHub before validating project Git information")
				}
				if name == "git" && strings.Join(args, " ") == "remote get-url origin" {
					return []byte(remote), nil
				}
				return commands.command(ctx, directory, name, args...)
			}
			if err := a.runIssueRound(context.Background(), p); err == nil {
				t.Fatal("admitted automation with missing or changed Git information")
			}
			chats, err := a.store.UserSessions(p.Issues.UserID, p.ID)
			if err != nil || len(chats) != 0 {
				t.Fatalf("invalid Git information created a chat: %v, %v", chats, err)
			}
			if _, err := os.Stat(filepath.Join(p.Path, "docs")); !os.IsNotExist(err) {
				t.Fatalf("invalid Git information touched local automation state: %v", err)
			}
			if _, err := os.Stat(issues.Directory(a.store.Root(), p.Path)); !os.IsNotExist(err) {
				t.Fatalf("invalid Git information created project data: %v", err)
			}
		})
	}
}

func TestIssueNetworkFailureKeepsLedgerByteIdenticalAndDoesNotCreateChat(t *testing.T) {
	a, p, f, runner := issueFixture(t)
	runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
		t.Fatal("agent started after failed remote read")
		return agent.AgentResult{}, nil
	}
	original := `{"version":1,"records":[],"custom":"Keep these exactly."}`
	path := filepath.Join(issues.Directory(a.store.Root(), p.Path), "issues.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	f.fail = "dial tcp: lookup api.github.com: no such host"
	err := a.runIssueRound(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "沙箱外联网") {
		t.Fatalf("network diagnosis missing: %v", err)
	}
	body, _ := os.ReadFile(path)
	if string(body) != original {
		t.Fatal("network failure changed ledger")
	}
	chats, _ := a.store.UserSessions(p.Issues.UserID, p.ID)
	if len(chats) != 0 {
		t.Fatal("network failure created chat/claim")
	}
	if _, err = os.Stat(filepath.Join(issues.Directory(a.store.Root(), p.Path), ".issues-automation.lock")); !os.IsNotExist(err) {
		t.Fatal("network failure leaked project lock")
	}
}

func TestNoEligibleLabelsOnlySyncsAndNoChatIsCreated(t *testing.T) {
	a, p, f, _ := issueFixture(t)
	f.noLabels = true
	if err := a.runIssueRound(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil || ledger.Records[1].Status != "open" {
		t.Fatal("unlabelled issue not synchronized")
	}
	chats, _ := a.store.UserSessions(p.Issues.UserID, p.ID)
	if len(chats) != 0 {
		t.Fatal("empty periodic chat created")
	}
	if _, err := os.Stat(filepath.Join(p.Path, "docs")); !os.IsNotExist(err) {
		t.Fatal("sync-only round created repository-local state")
	}
}

func TestIssueNetworkFailureDoesNotCreateJSONLedger(t *testing.T) {
	a, p, f, _ := issueFixture(t)
	f.fail = "dial tcp: lookup api.github.com: no such host"
	if err := a.runIssueRound(context.Background(), p); err == nil {
		t.Fatal("network failure treated as an empty GitHub snapshot")
	}
	if _, err := os.Stat(filepath.Join(issues.Directory(a.store.Root(), p.Path), "issues.json")); !os.IsNotExist(err) {
		t.Fatal("failed initial synchronization created JSON")
	}
	chats, err := a.store.UserSessions(p.Issues.UserID, p.ID)
	if err != nil || len(chats) != 0 {
		t.Fatalf("failed initial synchronization created a chat: %#v, %v", chats, err)
	}
}

func TestIssueRoundIgnoresOldMarkdownAndCreatesOnlyJSONLedger(t *testing.T) {
	a, p, f, _ := issueFixture(t)
	f.noLabels = true
	path := filepath.Join(p.Path, "docs", "issues.md")
	text := "# Old instructions\nissue #1 processing owned by somebody\n<!-- spool:issues:v2 -->"
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	oldJSON := filepath.Join(p.Path, "docs", "issues.json")
	if err := os.WriteFile(oldJSON, []byte("invalid obsolete copy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.runIssueRound(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil || ledger.Records[1].Status != "open" || ledger.Records[1].Owner != (issues.Owner{}) {
		t.Fatalf("Markdown claim affected new JSON: %#v, %v", ledger, err)
	}
	body, err := os.ReadFile(ledger.Path)
	if err != nil || strings.Contains(string(body), "Old instructions") || strings.Contains(string(body), "processing owned") {
		t.Fatalf("Markdown content copied to JSON: %s, %v", body, err)
	}
	body, err = os.ReadFile(path)
	if err != nil || string(body) != text {
		t.Fatal("automation touched old Markdown")
	}
	body, err = os.ReadFile(oldJSON)
	if err != nil || string(body) != "invalid obsolete copy" {
		t.Fatal("automation used or updated the obsolete repo-local ledger")
	}
}

func TestIssueLabelReservationHeldUntilActualAgentExitAndReleasedOnStop(t *testing.T) {
	a, p, _, runner := issueFixture(t)
	started, cleanup := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(cleanup) }) }
	t.Cleanup(unblock)
	var taskDirectory string
	runner.work = func(ctx context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		taskDirectory = request.WritableDirectories[0]
		close(started)
		<-ctx.Done()
		<-cleanup
		return agent.AgentResult{}, ctx.Err()
	}
	result := make(chan error, 1)
	go func() { result <- a.runIssueRound(context.Background(), p) }()
	<-started
	if err := a.runIssueRound(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	closing := make(chan struct{})
	go func() { _ = a.Close(); close(closing) }()
	time.Sleep(10 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(taskDirectory, ".issues-automation.lock")); err != nil {
		t.Fatal("stopped status prematurely released execution lock")
	}
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil || a.issueOwnerEnded(ledger.Records[1].Owner) {
		t.Fatal("still-exiting executor was considered finished")
	}
	unblock()
	<-closing
	if err := <-result; err == nil {
		t.Fatal("stopped task reported success")
	}
	if _, err := os.Stat(filepath.Join(taskDirectory, ".issues-automation.lock")); !os.IsNotExist(err) {
		t.Fatal("cleanup leaked execution lock")
	}
}

func TestIssueArtifactCallbackIsNoLongerAvailable(t *testing.T) {
	a, _, _, _ := issueFixture(t)
	cookie, csrf := loginRequest(t, a, "automation")
	w := apiRequest(a, "POST", "/api/automation/attach-artifact", `{"artifact_type":"pull_request","url":"https://github.com/fixture/issues/pull/20"}`, cookie, csrf)
	if w.Code != 404 {
		t.Fatalf("obsolete callback is still available: %d %s", w.Code, w.Body.String())
	}
}

func TestTaskAssigneesCanSeeSendStopAndApproveWithoutProjectWideSharing(t *testing.T) {
	a, p, _, runner := issueFixture(t)
	bob := testUser(t, a.store, "bob")
	other := testUser(t, a.store, "other")
	p.Members = append(p.Members, bob.ID, other.ID)
	p.Issues.AssigneeIDs = []string{p.Issues.UserID, bob.ID}
	var err error
	p, err = a.store.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := a.store.createAutomationChat(p.Issues, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	normal, err := a.store.CreateChat(p.Issues.UserID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if !a.store.CanControlChat(bob.ID, chat.ID) || a.store.CanAccessChat(other.ID, chat.ID) || a.store.CanControlChat(bob.ID, normal.ID) {
		t.Fatal("task-only permissions incorrect")
	}
	navigation, err := a.store.UserNavigation(bob.ID)
	if err != nil || len(navigation.Chats) != 1 || navigation.Chats[0].ID != chat.ID {
		t.Fatal("task missing from assignee navigation")
	}
	sessions, err := a.store.UserSessions(bob.ID, p.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatal("assignee cannot list private task chat")
	}
	cookie, csrf := loginRequest(t, a, "bob")
	otherCookie, otherCSRF := loginRequest(t, a, "other")
	runner.work = func(ctx context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		allowed, err := request.Approve(ctx, chatmodel.ApprovalRequest{Title: "Test shared approval"})
		if err != nil {
			return agent.AgentResult{}, err
		}
		if !allowed {
			return agent.AgentResult{}, errors.New("denied")
		}
		return agent.AgentResult{Text: "approved"}, nil
	}
	path := "/api/chats/" + chat.ID
	if w := apiRequest(a, "POST", path+"/messages", `{"message":"continue"}`, otherCookie, otherCSRF); w.Code != 404 {
		t.Fatal("non-assignee sent message")
	}
	if w := apiRequest(a, "POST", path+"/messages", `{"message":"continue"}`, cookie, csrf); w.Code != 202 {
		t.Fatalf("assignee send failed: %d %s", w.Code, w.Body.String())
	}
	turn := waitLiveTurn(t, a, chat.ID, func(v chatmodel.Turn) bool { return v.PendingApproval != nil })
	if turn.UserID != bob.ID {
		t.Fatalf("assignee turn attributed to %q instead of %q", turn.UserID, bob.ID)
	}
	body := `{"id":"` + turn.PendingApproval.ID + `","turn_id":"` + turn.ID + `","allow":true}`
	// Approval uses the same current task ACL as send and stop.
	ownerCookie, ownerCSRF := loginRequest(t, a, "automation")
	if w := apiRequest(a, "POST", path+"/approvals", body, ownerCookie, ownerCSRF); w.Code != 200 {
		t.Fatalf("assignee approval failed: %d %s", w.Code, w.Body.String())
	}
	approved := waitChat(t, a.store, chat.ID)
	if len(approved.Turns[0].Actions) != 1 || approved.Turns[0].Actions[0].UserID != p.Issues.UserID || approved.Turns[0].Actions[0].Kind != "approve" || approved.Turns[0].UserID != bob.ID {
		t.Fatalf("approval lost its distinct operator: %+v", approved.Turns)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.turnMu.Lock()
		active := a.activeTurns[chat.ID] != nil
		a.turnMu.Unlock()
		if !active {
			break
		}
		time.Sleep(time.Millisecond)
	}
	runner.work = func(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
		<-ctx.Done()
		return agent.AgentResult{}, ctx.Err()
	}
	// Wait for the follow-up's project-lock cleanup, not just persisted completion.
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(issues.Directory(a.store.Root(), p.Path), ".issues-automation.lock")); os.IsNotExist(err) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if w := apiRequest(a, "POST", path+"/messages", `{"message":"run again"}`, cookie, csrf); w.Code != 202 {
		t.Fatalf("second assignee turn: %d %s", w.Code, w.Body.String())
	}
	turn = waitLiveTurn(t, a, chat.ID, func(v chatmodel.Turn) bool { return v.Status == chatmodel.StatusRunning })
	if w := apiRequest(a, "POST", path+"/stop", `{"turn_id":"`+turn.ID+`"}`, cookie, csrf); w.Code != 200 {
		t.Fatalf("assignee stop failed: %d", w.Code)
	}
	stopped, _ := a.store.Get(chat.ID)
	if len(stopped.Turns[0].Actions) != 1 || stopped.Turns[0].Actions[0].Kind != "stop" || stopped.Turns[0].Actions[0].UserID != bob.ID {
		t.Fatalf("stop lost its operator: %+v", stopped.Turns)
	}
	// Editing responsibilities updates existing task chats immediately.
	p.Issues.AssigneeIDs = []string{p.Issues.UserID}
	if _, err = a.store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if a.store.CanControlChat(bob.ID, chat.ID) {
		t.Fatal("removed assignee retained control")
	}
	p.SharedChats = true
	if _, err = a.store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if !a.store.CanAccessChat(bob.ID, chat.ID) || !a.store.CanControlChat(bob.ID, chat.ID) || !a.store.CanControlChat(bob.ID, normal.ID) {
		t.Fatal("shared project did not grant session control")
	}
	p.MemberAccess = map[string]string{bob.ID: "read-only"}
	if _, err = a.store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if !a.store.CanAccessChat(bob.ID, chat.ID) || a.store.CanControlChat(bob.ID, chat.ID) || a.store.CanControlChat(bob.ID, normal.ID) {
		t.Fatal("read-only project member retained unassigned shared chat control")
	}
}

func TestSchedulerDueTimePauseAndUpdateAdmission(t *testing.T) {
	a, p, f, runner := issueFixture(t)
	f.noLabels = true
	if _, err := a.store.db.Exec(`UPDATE project_automations SET next_run_at=1 WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	a.updating = true
	a.scheduleIssues(context.Background(), time.Now())
	var count int
	_ = a.store.db.QueryRow(`SELECT count(*) FROM automation_runs`).Scan(&count)
	if count != 0 {
		t.Fatal("scheduler ran during update")
	}
	a.updating = false
	a.scheduleIssues(context.Background(), time.Now())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.automation.mu.Lock()
		active := len(a.automation.active) != 0
		a.automation.mu.Unlock()
		if !active {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_ = a.store.db.QueryRow(`SELECT count(*) FROM automation_runs`).Scan(&count)
	if count != len(p.Issues.IssueLabels()) {
		t.Fatalf("due label check count = %d", count)
	}
	a.scheduleIssues(context.Background(), time.Now())
	_ = a.store.db.QueryRow(`SELECT count(*) FROM automation_runs`).Scan(&count)
	if count != len(p.Issues.IssueLabels()) {
		t.Fatal("duplicate/catch-up run")
	}
	// A live scheduled worker blocks application updates, including preflight.
	f.mu.Lock()
	f.noLabels = false
	f.mu.Unlock()
	started, release := make(chan struct{}), make(chan struct{})
	runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
		close(started)
		<-release
		return agent.AgentResult{}, errors.New("verification unavailable")
	}
	_, _ = a.store.db.Exec(`UPDATE project_automations SET next_run_at=1 WHERE project_id=?`, p.ID)
	a.scheduleIssues(context.Background(), time.Now())
	<-started
	service := &Service{Store: a.store, app: a}
	if _, err := service.AcquireUpdate(); !errors.Is(err, ErrChatsRunning) {
		t.Fatal("update admitted while scheduled round active")
	}
	_, _ = a.store.db.Exec(`UPDATE project_automations SET next_run_at=1 WHERE project_id=?`, p.ID)
	a.scheduleIssues(context.Background(), time.Now())
	_ = a.store.db.QueryRow(`SELECT count(*) FROM automation_runs WHERE chat_id<>''`).Scan(&count)
	if count != 1 {
		t.Fatal("overlapping worker admitted")
	}
	close(release)
}

func TestAssigneeFollowupRefreshesClaimWithoutArtifactCapability(t *testing.T) {
	a, p, _, runner := issueFixture(t)
	runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
		return agent.AgentResult{}, errors.New("tests unavailable")
	}
	if err := a.runIssueRound(context.Background(), p); err == nil {
		t.Fatal("expected interrupted claim")
	}
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil {
		t.Fatal(err)
	}
	previous := ledger.Records[1].Owner
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		latest, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
		if err != nil {
			t.Fatal(err)
		}
		if latest.Records[1].Owner.Run == previous.Run || latest.Records[1].Owner.Chat != previous.Chat {
			t.Fatal("manual followup did not renew only the run owner")
		}
		if len(request.WritableDirectories) != 1 || !strings.HasPrefix(request.WritableDirectories[0], filepath.Join(filepath.Dir(latest.Path), "runs")+string(os.PathSeparator)) || !strings.Contains(request.Prompt, latest.Path) {
			t.Fatal("manual followup lost its scoped ledger access")
		}
		if !strings.Contains(request.Prompt, "负责人主动") || strings.Contains(request.Prompt, "attach_artifact") || strings.Contains(request.Prompt, "Authorization: Bearer") || !strings.Contains(request.Prompt, "prs 数组") || !strings.Contains(request.Prompt, "notes、validation 是字符串") || !strings.Contains(request.Prompt, "manual continuation") {
			t.Fatal("manual followup lost current scope or user prompt")
		}
		return agent.AgentResult{}, errors.New("still unable to verify")
	}
	cookie, csrf := loginRequest(t, a, "automation")
	w := apiRequest(a, "POST", "/api/chats/"+previous.Chat+"/messages", `{"message":"manual continuation"}`, cookie, csrf)
	if w.Code != 202 {
		t.Fatalf("followup rejected: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(issues.Directory(a.store.Root(), p.Path), ".issues-automation.lock")); os.IsNotExist(err) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = os.Stat(filepath.Join(issues.Directory(a.store.Root(), p.Path), ".issues-automation.lock")); !os.IsNotExist(err) {
		t.Fatal("manual followup did not release the project lock")
	}
}
