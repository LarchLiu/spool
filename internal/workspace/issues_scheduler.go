package workspace

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

type issueScheduler struct {
	mu       sync.Mutex
	active   map[string]bool
	instance string
	run      issues.Command
}

func (a *App) startIssueScheduler(instance string) {
	a.automation = &issueScheduler{instance: instance, run: a.store.RunIssueCommand, active: map[string]bool{}}
	a.turnMu.Lock()
	a.turns.Add(1)
	a.turnMu.Unlock()
	go func() {
		defer a.turns.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-a.shutdown:
				cancel()
			case <-ctx.Done():
			}
		}()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			a.scheduleIssues(ctx, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

type issueBatch struct {
	mu       sync.Mutex
	claimed  map[int]bool
	loaded   bool
	synced   bool
	snapshot []issues.Issue
	blocked  map[int]error
	err      error
}

func issueActiveKey(project, label string) string { return project + "\x00" + label }

func (a *App) scheduleIssues(ctx context.Context, now time.Time) {
	projects, err := a.store.Projects()
	if err != nil {
		log.Printf("issues scheduler: %v", err)
		return
	}
	for _, p := range projects {
		v := p.Issues
		if !v.Enabled || p.Archived || v.NextRunAt > now.UnixMilli() {
			continue
		}
		a.turnMu.Lock()
		if a.closing || a.updating || ctx.Err() != nil {
			a.turnMu.Unlock()
			return
		}
		res, e := a.store.db.Exec(`UPDATE project_automations SET next_run_at=?,last_run_at=? WHERE project_id=? AND next_run_at<=? AND body=?`, now.Add(time.Duration(v.IntervalMinutes)*time.Minute).UnixMilli(), now.UnixMilli(), p.ID, now.UnixMilli(), v.body)
		var n int64
		if e == nil {
			n, e = res.RowsAffected()
		}
		if e != nil || n != 1 {
			a.turnMu.Unlock()
			continue
		}
		batch := &issueBatch{claimed: map[int]bool{}}
		labels := v.IssueLabels()
		if len(labels) == 0 {
			labels = []string{""}
		}
		for _, label := range labels {
			key := issueActiveKey(p.ID, label)
			a.automation.mu.Lock()
			active := a.automation.active[key]
			if !active {
				a.automation.active[key] = true
			}
			a.automation.mu.Unlock()
			if active {
				continue
			}
			// Workers remain counted through actual provider cleanup and merge.
			a.runningTurns++
			a.turns.Add(1)
			go func(p Project, label, key string) {
				defer a.turns.Done()
				defer func() {
					a.turnMu.Lock()
					a.runningTurns--
					a.turnMu.Unlock()
					a.automation.mu.Lock()
					delete(a.automation.active, key)
					a.automation.mu.Unlock()
				}()
				err := a.runIssueLabelRound(ctx, p, label, batch)
				if errors.Is(err, issues.ErrLocked) || errors.Is(err, context.Canceled) {
					return
				}
				message := ""
				if err != nil {
					message = chatmodel.Clip(err.Error(), 8192)
				}
				// Successful labels must not erase a sibling's failure from this check.
				if _, e := a.store.db.Exec(`UPDATE project_automations SET last_error=COALESCE((SELECT error FROM automation_runs WHERE project_id=? AND status='failed' AND started_at>=project_automations.last_run_at ORDER BY finished_at DESC LIMIT 1),?) WHERE project_id=?`, p.ID, message, p.ID); e != nil {
					log.Printf("issues %s: could not save run status: %v", p.Name, e)
				}
				if message != "" && message != p.Issues.LastError {
					log.Printf("项目 %s 的 issues 定时任务 [%s]：%s", p.Name, label, message)
				}
			}(p, label, key)
		}
		a.turnMu.Unlock()
	}
}

func (a *App) issueRunEnded(id string) bool {
	var status string
	err := a.store.db.QueryRow(`SELECT status FROM automation_runs WHERE id=?`, id).Scan(&status)
	return err == nil && (status == "finished" || status == "failed")
}

func (a *App) runIssueRound(ctx context.Context, p Project) error {
	return a.runIssueLabelRound(ctx, p, "", nil)
}

func (a *App) runIssueLabelRound(ctx context.Context, p Project, label string, batch *issueBatch) (runErr error) {
	v := p.Issues
	if !a.store.CanAccessProject(v.UserID, p.ID) {
		return fmt.Errorf("自动化聊天归属账号已禁用或不是项目成员，请在 Projects 中重新配置")
	}
	if !a.enabled(v.Backend) {
		return fmt.Errorf("定时任务所选 agent 未启用或尚未安装")
	}
	runID := chatmodel.NewID()
	owner := issues.NewOwner(a.automation.instance, runID)
	ended := a.issueOwnerEnded
	checkout, err := issues.Inspect(ctx, a.automation.run, p.Path, v.Repository)
	if err != nil {
		return err
	}
	cliHint, err := a.store.issueGitHubCLIHint()
	if err != nil {
		return err
	}
	v.Repository = checkout.Repository
	directory, err := issues.PrepareDirectory(a.store.Root(), checkout.Root)
	if err != nil {
		return err
	}
	lock, err := a.acquireIssueState(ctx, directory, owner)
	if err != nil {
		return err
	}
	defer func() {
		if lock != nil {
			runErr = errors.Join(runErr, lock.Release())
		}
	}()
	if _, err = a.store.db.Exec(`INSERT INTO automation_runs(id,project_id,instance,status,started_at) VALUES(?,?,?,'running',?)`, runID, p.ID, owner.Instance, time.Now().UnixMilli()); err != nil {
		return err
	}
	defer func() {
		status := "finished"
		message := ""
		if runErr != nil {
			status = "failed"
			message = chatmodel.Clip(runErr.Error(), 8192)
		}
		if _, e := a.store.db.Exec(`UPDATE automation_runs SET status=?,finished_at=?,error=? WHERE id=?`, status, time.Now().UnixMilli(), message, runID); e != nil {
			runErr = errors.Join(runErr, e)
		}
	}()
	ledger, err := issues.Load(directory)
	if err != nil {
		return err
	}
	github := issues.GitHub{Run: a.automation.run, Directory: p.Path, Repository: v.Repository}
	snapshot, blocked, synced, err := a.issueBatchSnapshot(ctx, github, ledger, batch)
	if err != nil {
		return err
	}
	if !synced {
		ledger.Sync(snapshot, v.IssueLabels())
	}
	candidates := []issues.Issue{}
	var recoveryErrors []error
	for _, issue := range snapshot {
		if label != "" && !issueHasLabel(issue, label) {
			continue
		}
		if e := blocked[issue.Number]; e != nil {
			recoveryErrors = append(recoveryErrors, e)
			continue
		}
		if batch != nil {
			batch.mu.Lock()
			seen := batch.claimed[issue.Number]
			batch.mu.Unlock()
			if seen {
				continue
			}
		}
		candidates = append(candidates, issue)
	}
	selected, resume := ledger.Select(candidates, v.IssueLabels(), ended)
	if selected == nil {
		return errors.Join(append(recoveryErrors, saveIssueBatch(ledger, batch))...)
	}

	if resume && (ledger.Records[selected.Number].Branch == "" || ledger.Records[selected.Number].Worktree == "") {
		return fmt.Errorf("issue #%d 的未完成认领缺少分支或 worktree，请先核实台账", selected.Number)
	}
	selected.Comments, err = github.Comments(ctx, selected.Number)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !a.store.CanAccessProject(v.UserID, p.ID) {
		return fmt.Errorf("项目权限已改变，未认领任务")
	}
	if v.body != "" {
		current, e := a.store.IssuesAutomation(p.ID)
		if e != nil {
			return e
		}
		if current.body != v.body {
			return fmt.Errorf("issue automation settings changed before admission")
		}
	}
	options := chatmodel.CodexOptions{Model: v.Model, ReasoningEffort: v.Effort}
	if err = a.validateSelection(ctx, chatmodel.Session{Backend: v.Backend, ProjectID: p.ID}, options); err != nil {
		return err
	}
	if v.Permission != "" {
		catalog, e := a.permissionCatalog(ctx, chatmodel.Session{Backend: v.Backend, ProjectID: p.ID})
		if e != nil {
			return e
		}
		if e = catalog.Validate(v.Permission); e != nil {
			return e
		}
	}
	chat, err := a.store.createAutomationChat(v, p.ID)
	if err != nil {
		return err
	}
	owner.Chat = chat.ID
	if err = lock.SetOwner(owner); err != nil {
		return err
	}
	record := ledger.Records[selected.Number]
	if !resume || len(v.IssueLabels()) == 0 || len(record.Categories) == 0 {
		record.Categories = issues.Categories(*selected, v.IssueLabels())
	}
	if !resume {
		record.Branch = fmt.Sprintf("codex/issue-%d-fix", selected.Number)
		record.Worktree = filepath.Join(filepath.Dir(checkout.Root), ".spool-worktrees", filepath.Base(checkout.Root), fmt.Sprintf("issue-%d", selected.Number))
	}
	record.History = append(record.History, time.Now().UTC().Format(time.RFC3339)+" claimed by issues run "+runID+" chat "+chat.ID)
	record.Owner, record.Status, record.ClaimedAt = owner, "processing", time.Now().UTC().Format(time.RFC3339)
	task, err := issues.PrepareTask(directory, *record)
	if err != nil {
		return err
	}
	if err = saveIssueBatch(ledger, batch); err != nil {
		return err
	}
	if batch != nil {
		batch.mu.Lock()
		batch.claimed[selected.Number] = true
		batch.mu.Unlock()
	}
	if err = lock.Release(); err != nil {
		lock = nil
		return err
	}
	lock = nil
	defer func() {
		runErr = errors.Join(runErr, a.finishIssueTask(ctx, github, directory, selected.Number, owner, v.IssueLabels(), runErr))
	}()
	execution, err := issues.Acquire(filepath.Dir(task.Path), owner, ended)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, execution.Release()) }()
	title := fmt.Sprintf("Issues #%d · %s", selected.Number, selected.Title)
	if _, err = a.store.db.Exec(`UPDATE sessions SET title=? WHERE id=?`, chatmodel.Clip(title, 160), chat.ID); err != nil {
		return err
	}
	if _, err = a.store.db.Exec(`UPDATE automation_runs SET chat_id=? WHERE id=?`, chat.ID, runID); err != nil {
		return err
	}
	if _, err = a.store.db.Exec(`UPDATE project_automations SET last_chat_id=? WHERE project_id=?`, chat.ID, p.ID); err != nil {
		return err
	}
	prompt, err := issuePrompt(p, checkout, *selected, *record, resume, cliHint, ledger.Path, task.Path)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	if err = a.startTurnWithCallback(chat, messageInput{Message: prompt, PermissionMode: v.Permission, writableDirectories: []string{filepath.Dir(task.Path)}}, options, func(e error) { done <- e }); err != nil {
		return err
	}
	// Keep label reservations until actual cleanup and task import, not merely
	// the persisted chat status or the next timer tick.
	err = <-done
	if err != nil {
		return fmt.Errorf("issue #%d 执行未完成，保留 processing 与恢复记录：%w", selected.Number, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func issueHasLabel(issue issues.Issue, label string) bool {
	for _, value := range issue.Labels {
		if value.Name == label {
			return true
		}
	}
	return false
}
