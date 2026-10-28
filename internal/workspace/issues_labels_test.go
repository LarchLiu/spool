package workspace

import (
	"context"
	"encoding/json"
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

func TestIssueLabelsSettingsPersistArbitraryNamesAndNoFixedCount(t *testing.T) {
	a, p, _, _ := issueFixture(t)
	labels := []string{" UI / v2 ", "bug,urgent", "后端", "UPPER", "UPPER"}
	for i := 0; i < 80; i++ {
		labels = append(labels, strings.Repeat("x", i+1))
	}
	p.Issues.Labels = labels
	saved, err := a.store.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := a.store.IssuesAutomation(p.ID)
	if err != nil || len(loaded.Labels) != 84 || loaded.Labels[0] != "UI / v2" || loaded.Labels[1] != "bug,urgent" || len(saved.Issues.Labels) != 84 {
		t.Fatalf("labels were not normalized and persisted: %#v, %v", loaded.Labels, err)
	}
	for _, labels := range [][]string{nil, {}, {"  ", ""}} {
		p.Issues.Labels = labels
		if _, err := a.store.SaveProject(p); err != nil {
			t.Fatalf("enabled schedule rejected empty labels: %v", err)
		}
		loaded, err = a.store.IssuesAutomation(p.ID)
		if err != nil || loaded.Labels == nil || len(loaded.IssueLabels()) != 0 || !strings.Contains(loaded.body, `"labels":[]`) {
			t.Fatalf("empty labels did not persist: %#v, %v", loaded, err)
		}
	}
	p.Issues.Labels = []string{"one\ntwo"}
	if _, err := a.store.SaveProject(p); err == nil {
		t.Fatal("accepted a multiline label")
	}
	// Missing labels also mean unfiltered serial work, and the scheduler must
	// still use the original stored body for its exact-body admission check.
	if _, err := a.store.db.Exec(`UPDATE project_automations SET body=json_remove(body,'$.labels') WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = a.store.IssuesAutomation(p.ID)
	if err != nil || len(loaded.IssueLabels()) != 0 || loaded.body == "" {
		t.Fatal("missing-label defaults or stored body were lost")
	}
	defaults, err := a.store.IssuesAutomation("not-yet-configured")
	if err != nil || defaults.Labels == nil || len(defaults.IssueLabels()) != 0 || len((IssuesAutomation{}).IssueLabels()) != 0 {
		t.Fatalf("new schedules should default to empty labels: %#v, %v", defaults, err)
	}
}

func setIssueLabelSnapshot(t *testing.T, a *App, commands *issueCommands, snapshot string) {
	t.Helper()
	var pages [][]map[string]any
	if err := json.Unmarshal([]byte(snapshot), &pages); err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		for _, issue := range page {
			issue["html_url"] = fmt.Sprintf("https://github.com/fixture/issues/issues/%.0f", issue["number"])
		}
	}
	body, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	a.automation.run = func(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
		if name == "gh" && strings.Contains(strings.Join(args, " "), "/issues?state=all") {
			return body, nil
		}
		return commands.command(ctx, directory, name, args...)
	}
}

func waitIssueWorkers(t *testing.T, a *App) {
	waitIssueWorkerCount(t, a, 0)
}

func waitIssueWorkerCount(t *testing.T, a *App, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.automation.mu.Lock()
		n := len(a.automation.active)
		a.automation.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("issue worker count did not reach %d", want)
}

func runDueIssueCheck(t *testing.T, a *App, project string) {
	t.Helper()
	if _, err := a.store.db.Exec(`UPDATE project_automations SET next_run_at=1 WHERE project_id=?`, project); err != nil {
		t.Fatal(err)
	}
	a.scheduleIssues(context.Background(), time.Now())
}

func waitStartedIssue(t *testing.T, started <-chan int) int {
	t.Helper()
	select {
	case number := <-started:
		return number
	case <-time.After(5 * time.Second):
		t.Fatal("issue agent did not start")
		return 0
	}
}

func TestIssueLabelsEmptyScheduleRunsSeriallyWithoutFiltering(t *testing.T) {
	for _, setting := range []string{"empty", "missing", "null"} {
		t.Run(setting, func(t *testing.T) {
			a, p, commands, runner := issueFixture(t)
			p.Issues.Labels = nil
			var err error
			p, err = a.store.SaveProject(p)
			if err != nil {
				t.Fatal(err)
			}
			if setting == "missing" {
				_, err = a.store.db.Exec(`UPDATE project_automations SET body=json_remove(body,'$.labels') WHERE project_id=?`, p.ID)
			} else if setting == "null" {
				_, err = a.store.db.Exec(`UPDATE project_automations SET body=json_set(body,'$.labels',NULL) WHERE project_id=?`, p.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			setIssueLabelSnapshot(t, a, commands, `[[{"number":1,"state":"open","labels":[]},{"number":2,"state":"open","labels":[{"name":"anything"}]},{"number":3,"state":"open","labels":[{"name":"web"},{"name":"server"}]}]]`)
			started, release := make(chan int, 8), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			runner.work = func(ctx context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
				task, err := issues.Load(request.WritableDirectories[0])
				if err != nil {
					return agent.AgentResult{}, err
				}
				for _, record := range task.Records {
					if len(record.Categories) != 0 || !strings.Contains(request.Prompt, `"reserved_labels": []`) {
						return agent.AgentResult{}, errors.New("unfiltered task did not reserve the global slot")
					}
					started <- record.Number
				}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return agent.AgentResult{}, errors.New("unfinished for recovery")
			}
			runDueIssueCheck(t, a, p.ID)
			if number := waitStartedIssue(t, started); number != 1 {
				t.Fatalf("did not select the unlabelled issue first: %d", number)
			}
			runDueIssueCheck(t, a, p.ID)
			var chats int
			if err := a.store.db.QueryRow(`SELECT count(*) FROM automation_chats`).Scan(&chats); err != nil || chats != 1 {
				t.Fatalf("next timer tick duplicated live global work: %d, %v", chats, err)
			}
			unblock()
			waitIssueWorkers(t, a)
			runDueIssueCheck(t, a, p.ID)
			if number := waitStartedIssue(t, started); number != 1 {
				t.Fatalf("did not prioritize ended unfinished work: %d", number)
			}
			waitIssueWorkers(t, a)
			setIssueLabelSnapshot(t, a, commands, `[[{"number":1,"state":"closed","labels":[]},{"number":2,"state":"open","labels":[{"name":"anything"}]},{"number":3,"state":"open","labels":[{"name":"web"},{"name":"server"}]}]]`)
			runDueIssueCheck(t, a, p.ID)
			if number := waitStartedIssue(t, started); number != 2 {
				t.Fatalf("arbitrary issue labels were not ignored: %d", number)
			}
			waitIssueWorkers(t, a)
			ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
			if err != nil || ledger.Records[1].Status != "close" || ledger.Records[2].Status != "processing" || ledger.Records[3].Status != "open" {
				t.Fatalf("serial schedule did not preserve issue states: %#v, %v", ledger, err)
			}
		})
	}
}

func TestIssueLabelsModeSwitchWaitsForLiveTasks(t *testing.T) {
	for _, test := range []struct {
		name           string
		initial        []string
		next           []string
		wantNumber     int
		wantCategories []string
	}{
		{"global-to-labels", nil, []string{"alpha", "beta"}, 1, []string{"alpha"}},
		{"global-to-new-label", nil, []string{"beta"}, 2, []string{"beta"}},
		{"labels-to-global", []string{"alpha", "beta"}, nil, 1, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, p, commands, runner := issueFixture(t)
			p.Issues.Labels = test.initial
			initialCount := len(test.initial)
			if initialCount == 0 {
				initialCount = 1
			}
			var err error
			p, err = a.store.SaveProject(p)
			if err != nil {
				t.Fatal(err)
			}
			setIssueLabelSnapshot(t, a, commands, `[[{"number":1,"state":"open","labels":[{"name":"alpha"}]},{"number":2,"state":"open","labels":[{"name":"beta"}]}]]`)
			started, release := make(chan int, 8), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			runner.work = func(ctx context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
				task, err := issues.Load(request.WritableDirectories[0])
				if err != nil {
					return agent.AgentResult{}, err
				}
				for _, record := range task.Records {
					started <- record.Number
				}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return agent.AgentResult{}, errors.New("unfinished for mode switch")
			}
			runDueIssueCheck(t, a, p.ID)
			for i := 0; i < initialCount; i++ {
				waitStartedIssue(t, started)
			}
			p.Issues.Labels = test.next
			p, err = a.store.SaveProject(p)
			if err != nil {
				t.Fatal(err)
			}
			runDueIssueCheck(t, a, p.ID)
			waitIssueWorkerCount(t, a, initialCount)
			var chats int
			if err := a.store.db.QueryRow(`SELECT count(*) FROM automation_chats`).Scan(&chats); err != nil || chats != initialCount {
				t.Fatalf("mode switch bypassed live reservations: %d, %v", chats, err)
			}
			unblock()
			waitIssueWorkers(t, a)
			if err := a.runIssueRound(context.Background(), p); err == nil || !strings.Contains(err.Error(), "unfinished for mode switch") {
				t.Fatalf("ended claim could not resume in the new mode: %v", err)
			}
			if number := waitStartedIssue(t, started); number != test.wantNumber {
				t.Fatalf("wrong task after mode switch: got %d, want %d", number, test.wantNumber)
			}
			ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
			if err != nil {
				t.Fatal(err)
			}
			categories := ledger.Records[test.wantNumber].Categories
			if strings.Join(categories, "\x00") != strings.Join(test.wantCategories, "\x00") {
				t.Fatalf("task kept the previous mode's reservation: %v", categories)
			}
		})
	}
}

func TestIssueLabelsRunConcurrentlyAcrossIntervalsAndMergeIsolatedProgress(t *testing.T) {
	a, p, commands, runner := issueFixture(t)
	p.Issues.Labels = []string{"UI / v2", "后端", "qa,urgent"}
	var err error
	p, err = a.store.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	setIssueLabelSnapshot(t, a, commands, `[[
		{"number":1,"state":"open","title":"UI","labels":[{"name":"UI / v2"}]},
		{"number":2,"state":"open","title":"API","labels":[{"name":"后端"}]},
		{"number":3,"state":"open","title":"QA","labels":[{"name":"qa,urgent"}]},
		{"number":4,"state":"open","title":"UI second","labels":[{"name":"UI / v2"}]}
	]]`)
	started := make(chan int, 8)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	runner.work = func(ctx context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		task, e := issues.Load(request.WritableDirectories[0])
		if e != nil {
			return agent.AgentResult{}, e
		}
		var record *issues.Record
		for _, r := range task.Records {
			record = r
		}
		if record == nil {
			return agent.AgentResult{}, errors.New("task record missing")
		}
		record.Notes = "isolated progress " + record.Title
		record.Validation = "fixture test passed"
		if e = task.Save(); e != nil {
			return agent.AgentResult{}, e
		}
		started <- record.Number
		select {
		case <-release:
		case <-ctx.Done():
		}
		return agent.AgentResult{}, errors.New("fixture intentionally unfinished")
	}
	due := func() {
		t.Helper()
		if _, e := a.store.db.Exec(`UPDATE project_automations SET next_run_at=1 WHERE project_id=?`, p.ID); e != nil {
			t.Fatal(e)
		}
		a.scheduleIssues(context.Background(), time.Now())
	}
	due()
	seen := map[int]bool{}
	for len(seen) < 3 {
		select {
		case n := <-started:
			seen[n] = true
		case <-time.After(5 * time.Second):
			t.Fatal("labels did not start concurrently")
		}
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Fatalf("wrong simultaneous tasks: %v", seen)
	}
	due()
	time.Sleep(50 * time.Millisecond)
	var chats int
	if e := a.store.db.QueryRow(`SELECT count(*) FROM automation_chats`).Scan(&chats); e != nil || chats != 3 {
		t.Fatalf("timer duplicated live tasks: %d %v", chats, e)
	}
	unblock()
	waitIssueWorkers(t, a)
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3} {
		if ledger.Records[n].Notes != "isolated progress "+ledger.Records[n].Title || ledger.Records[n].Validation != "fixture test passed" || ledger.Records[n].Status != "processing" {
			t.Fatalf("lost concurrent progress #%d: %#v", n, ledger.Records[n])
		}
	}
	if ledger.Records[4].Status != "open" {
		t.Fatal("same-label second task was admitted")
	}
}

func TestMultiLabelClaimReservesAllLabelsWhileOtherLabelsContinue(t *testing.T) {
	a, p, commands, runner := issueFixture(t)
	p.Issues.Labels = []string{"alpha", "beta", "gamma"}
	var err error
	p, err = a.store.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	setIssueLabelSnapshot(t, a, commands, `[[{"number":1,"state":"open","labels":[{"name":"alpha"},{"name":"beta"}]},{"number":2,"state":"open","labels":[{"name":"beta"}]},{"number":3,"state":"open","labels":[{"name":"gamma"}]}]]`)
	started, release := make(chan string, 4), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	runner.work = func(ctx context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		started <- request.ChatID
		select {
		case <-release:
		case <-ctx.Done():
		}
		return agent.AgentResult{}, errors.New("unfinished")
	}
	first := make(chan error, 1)
	go func() { first <- a.runIssueLabelRound(context.Background(), p, "alpha", nil) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("alpha not started")
	}
	if err := a.runIssueLabelRound(context.Background(), p, "beta", nil); err != nil {
		t.Fatal(err)
	}
	third := make(chan error, 1)
	go func() { third <- a.runIssueLabelRound(context.Background(), p, "gamma", nil) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("unrelated gamma was blocked")
	}
	unblock()
	<-first
	<-third
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil || ledger.Records[2].Status != "open" || len(ledger.Records[1].Categories) != 2 {
		t.Fatalf("multi-label occupancy failed: %#v %v", ledger, err)
	}
}

func TestInvalidTaskValidationCannotCorruptSharedLedgerOrBlockOtherLabels(t *testing.T) {
	a, p, commands, runner := issueFixture(t)
	p.Issues.Labels = []string{"alpha", "beta"}
	var err error
	p, err = a.store.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	setIssueLabelSnapshot(t, a, commands, `[[{"number":1,"state":"open","labels":[{"name":"alpha"}]},{"number":2,"state":"open","labels":[{"name":"beta"}]}]]`)
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		path := filepath.Join(request.WritableDirectories[0], "issues.json")
		body, e := os.ReadFile(path)
		if e != nil {
			return agent.AgentResult{}, e
		}
		var doc map[string]any
		if e = json.Unmarshal(body, &doc); e != nil {
			return agent.AgentResult{}, e
		}
		r := doc["records"].([]any)[0].(map[string]any)
		if r["number"] == float64(1) {
			r["validation"] = []string{"wrong type"}
		} else {
			r["notes"] = "beta can continue"
		}
		body, e = json.Marshal(doc)
		if e != nil {
			return agent.AgentResult{}, e
		}
		if e = os.WriteFile(path, body, 0600); e != nil {
			return agent.AgentResult{}, e
		}
		return agent.AgentResult{}, errors.New("unfinished")
	}
	if err := a.runIssueLabelRound(context.Background(), p, "alpha", nil); err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("bad task type was not rejected: %v", err)
	}
	if err := a.runIssueLabelRound(context.Background(), p, "beta", nil); err == nil || strings.Contains(err.Error(), "validation") {
		t.Fatalf("unrelated beta was blocked: %v", err)
	}
	ledger, err := issues.Load(issues.Directory(a.store.Root(), p.Path))
	if err != nil || ledger.Records[1].Validation != "" || ledger.Records[2].Notes != "beta can continue" {
		t.Fatalf("shared ledger was corrupted: %#v %v", ledger, err)
	}
}

func TestEndedIssueTaskProgressIsImportedBeforeRecovery(t *testing.T) {
	a, p, _, runner := issueFixture(t)
	directory, err := issues.PrepareDirectory(a.store.Root(), p.Path)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := issues.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	owner := issues.NewOwner(a.automation.instance, "ended-run")
	owner.Chat = "old-chat"
	ledger.Records[1] = &issues.Record{Number: 1, Status: "processing", Owner: owner, Categories: []string{"web", "server"}, Branch: "codex/issue-1-fix", Worktree: "/isolated/old"}
	if err := ledger.Save(); err != nil {
		t.Fatal(err)
	}
	task, err := issues.PrepareTask(directory, *ledger.Records[1])
	if err != nil {
		t.Fatal(err)
	}
	task.Records[1].Notes = "crash progress not yet in shared ledger"
	task.Records[1].Worktree = "/isolated/actual"
	if err := task.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec(`INSERT INTO automation_runs(id,project_id,instance,status,started_at) VALUES(?,?,?,'failed',1)`, owner.Run, p.ID, owner.Instance); err != nil {
		t.Fatal(err)
	}
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		if !strings.Contains(request.Prompt, "crash progress not yet") || !strings.Contains(request.Prompt, "/isolated/actual") || !strings.Contains(request.Prompt, `"resume": true`) {
			return agent.AgentResult{}, errors.New("recovery lost task progress")
		}
		return agent.AgentResult{}, errors.New("recovery verified")
	}
	if err := a.runIssueRound(context.Background(), p); err == nil || !strings.Contains(err.Error(), "recovery verified") {
		t.Fatalf("task recovery failed: %v", err)
	}
}

func TestIssueFollowupRechecksNewlyMatchingLabelsBeforeAdmission(t *testing.T) {
	t.Run("labels", func(t *testing.T) { testIssueFollowupReservation(t, false) })
	t.Run("global", func(t *testing.T) { testIssueFollowupReservation(t, true) })
}

func testIssueFollowupReservation(t *testing.T, unfiltered bool) {
	t.Helper()
	a, p, commands, runner := issueFixture(t)
	p.Issues.Labels = []string{"alpha", "beta"}
	if unfiltered {
		p.Issues.Labels = nil
	}
	var err error
	p, err = a.store.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := a.store.createAutomationChat(p.Issues, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := issues.PrepareDirectory(a.store.Root(), p.Path)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := issues.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	owner := issues.NewOwner(a.automation.instance, "previous-run")
	owner.Chat = chat.ID
	ledger.Records[1] = &issues.Record{Number: 1, Status: "processing", Owner: owner, Categories: []string{"alpha"}, Branch: "codex/issue-1-fix", Worktree: "/isolated/1"}
	ledger.Records[2] = &issues.Record{Number: 2, Status: "processing", Owner: issues.NewOwner(a.automation.instance, "still-running"), Categories: []string{"beta"}, Branch: "codex/issue-2-fix", Worktree: "/isolated/2"}
	if err := ledger.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.db.Exec(`INSERT INTO automation_runs(id,project_id,instance,status,started_at) VALUES(?,?,?,'failed',1)`, owner.Run, p.ID, owner.Instance); err != nil {
		t.Fatal(err)
	}
	snapshot := `[[{"number":1,"state":"open","labels":[{"name":"alpha"},{"name":"beta"}]},{"number":2,"state":"open","labels":[{"name":"beta"}]}]]`
	if unfiltered {
		snapshot = `[[{"number":1,"state":"open","labels":[{"name":"alpha"}]},{"number":2,"state":"open","labels":[{"name":"beta"}]}]]`
	}
	setIssueLabelSnapshot(t, a, commands, snapshot)
	runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
		t.Error("follow-up bypassed the newly occupied label")
		return agent.AgentResult{}, nil
	}
	if err := a.startIssueFollowup(chat, messageInput{Message: "continue"}, chatmodel.CodexOptions{}); !errors.Is(err, issues.ErrLocked) {
		t.Fatalf("follow-up did not respect current labels: %v", err)
	}
}
