package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

type issueScheduler struct {
	mu                    sync.Mutex
	active                map[string]bool
	scopes                map[string]*issueArtifactScope
	instance, callbackURL string
	run                   issues.Command
}

type issueArtifactScope struct {
	chat, branch, repository, directory string
	ctx                                 context.Context
	mu                                  sync.Mutex
	closed                              bool
}

func (a *App) startIssueScheduler(instance, address string) {
	host, port, _ := net.SplitHostPort(address)
	if host == "::" {
		host = "::1"
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	a.automation = &issueScheduler{instance: instance, callbackURL: "http://" + net.JoinHostPort(host, port) + "/api/automation/attach-artifact", run: issues.Run, active: map[string]bool{}, scopes: map[string]*issueArtifactScope{}}
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
		a.automation.mu.Lock()
		if a.automation.active[p.ID] {
			a.automation.mu.Unlock()
			a.turnMu.Unlock()
			continue
		}
		a.automation.active[p.ID] = true
		a.automation.mu.Unlock()
		body, _ := json.Marshal(v)
		res, e := a.store.db.Exec(`UPDATE project_automations SET next_run_at=?,last_run_at=? WHERE project_id=? AND next_run_at<=? AND body=?`, now.Add(time.Duration(v.IntervalMinutes)*time.Minute).UnixMilli(), now.UnixMilli(), p.ID, now.UnixMilli(), string(body))
		var n int64
		if e == nil {
			n, e = res.RowsAffected()
		}
		if e != nil || n != 1 {
			a.automation.mu.Lock()
			delete(a.automation.active, p.ID)
			a.automation.mu.Unlock()
			a.turnMu.Unlock()
			continue
		}
		// Admission is counted alongside user turns, so updates and Stop cannot
		// race preflight GitHub reads or release a lock underneath a worker.
		a.runningTurns++
		a.turns.Add(1)
		a.turnMu.Unlock()
		go func(p Project) {
			defer a.turns.Done()
			defer func() {
				a.turnMu.Lock()
				a.runningTurns--
				a.turnMu.Unlock()
				a.automation.mu.Lock()
				delete(a.automation.active, p.ID)
				a.automation.mu.Unlock()
			}()
			err := a.runIssueRound(ctx, p)
			if errors.Is(err, issues.ErrLocked) || errors.Is(err, context.Canceled) {
				return
			}
			message := ""
			if err != nil {
				message = chatmodel.Clip(err.Error(), 8192)
			}
			if _, e := a.store.db.Exec(`UPDATE project_automations SET last_error=? WHERE project_id=?`, message, p.ID); e != nil {
				log.Printf("issues %s: could not save run status: %v", p.Name, e)
			}
			if message != "" && message != p.Issues.LastError {
				log.Printf("项目 %s 的 issues 定时任务：%s", p.Name, message)
			}
		}(p)
	}
}

func (a *App) issueRunEnded(id string) bool {
	var status string
	err := a.store.db.QueryRow(`SELECT status FROM automation_runs WHERE id=?`, id).Scan(&status)
	return err == nil && (status == "finished" || status == "failed")
}

func (a *App) runIssueRound(ctx context.Context, p Project) (runErr error) {
	v := p.Issues
	if !a.store.CanAccessProject(v.UserID, p.ID) {
		return fmt.Errorf("自动化聊天归属账号已禁用或不是项目成员，请在 Projects 中重新配置")
	}
	if !a.enabled(v.Backend) {
		return fmt.Errorf("定时任务所选 agent 未启用或尚未安装")
	}
	runID := chatmodel.NewID()
	owner := issues.NewOwner(a.automation.instance, runID)
	ended := func(o issues.Owner) bool {
		a.turnMu.Lock()
		active := a.activeTurns[o.Chat] != nil
		a.turnMu.Unlock()
		return !active && o.Ended(a.automation.instance, a.issueRunEnded)
	}
	checkout, err := issues.Inspect(ctx, a.automation.run, p.Path, v.Repository)
	if err != nil {
		return err
	}
	v.Repository = checkout.Repository
	lock, err := issues.Acquire(checkout.Root, owner, ended)
	if err != nil {
		return err
	}
	defer func() {
		if e := lock.Release(); e != nil {
			runErr = errors.Join(runErr, e)
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
	ledger, err := issues.Load(checkout.Root)
	if err != nil {
		return err
	}
	github := issues.GitHub{Run: a.automation.run, Directory: p.Path, Repository: v.Repository}
	snapshot, err := github.Snapshot(ctx, ledger)
	if err != nil {
		return err
	}
	// A human may have edited the ledger during the network round trip.
	fresh, err := issues.Load(checkout.Root)
	if err != nil {
		return err
	}
	if !ledger.SameRevision(fresh) {
		return fmt.Errorf("共享台账在远端同步期间有人工更新，本轮不认领，下一轮重新同步")
	}
	ledger = fresh
	ledger.Sync(snapshot)
	selected, resume := ledger.Select(snapshot, ended)
	if selected == nil {
		// Recover attachments from a previously interrupted callback/finalizer,
		// without opening another chat for an already-open PR.
		for _, issue := range snapshot {
			record := ledger.Records[issue.Number]
			if record.Owner.Automation != "issues" || record.Owner.Chat == "" {
				continue
			}
			for _, pr := range issue.PRs {
				if pr.Open() && pr.Head == record.Branch && pr.Base == "dev" {
					var count int
					if e := a.store.db.QueryRow(`SELECT count(*) FROM automation_runs WHERE chat_id=? AND project_id=?`, record.Owner.Chat, p.ID).Scan(&count); e != nil {
						return e
					}
					if count > 0 {
						if e := a.store.attachPR(record.Owner.Chat, pr.URL); e != nil {
							return e
						}
					}
				}
			}
		}
		return ledger.Save()
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
	if !resume {
		record.Branch = fmt.Sprintf("codex/issue-%d-fix", selected.Number)
		record.Worktree = filepath.Join(filepath.Dir(checkout.Root), ".spool-worktrees", filepath.Base(checkout.Root), fmt.Sprintf("issue-%d", selected.Number))
	}
	record.History = append(record.History, time.Now().UTC().Format(time.RFC3339)+" claimed by issues run "+runID+" chat "+chat.ID)
	record.Owner, record.Status, record.ClaimedAt = owner, "processing", time.Now().UTC().Format(time.RFC3339)
	if err = ledger.Save(); err != nil {
		return err
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, noteIssueFailure(checkout.Root, selected.Number, owner, runErr))
		}
	}()
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
	token := randomToken()
	artifactContext, cancelArtifacts := context.WithCancel(ctx)
	scope := &issueArtifactScope{chat: chat.ID, branch: record.Branch, repository: v.Repository, directory: p.Path, ctx: artifactContext}
	a.automation.mu.Lock()
	a.automation.scopes[token] = scope
	a.automation.mu.Unlock()
	defer func() {
		cancelArtifacts()
		a.automation.mu.Lock()
		delete(a.automation.scopes, token)
		a.automation.mu.Unlock()
		scope.mu.Lock()
		scope.closed = true
		scope.mu.Unlock()
	}()
	prompt, err := issuePrompt(p, checkout, *selected, *record, resume, a.automation.callbackURL, token)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	if err = a.startTurnWithCallback(chat, messageInput{Message: prompt, PermissionMode: v.Permission}, options, func(e error) { done <- e }); err != nil {
		return err
	}
	// Stop cancels both sides. Still wait for actual turn cleanup before dropping
	// the project lock; a persisted "stopped" chat alone is not proof of exit.
	err = <-done
	if err != nil {
		return fmt.Errorf("issue #%d 执行未完成，保留 processing 与恢复记录：%w", selected.Number, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return a.finishIssueRound(ctx, github, checkout.Root, selected.Number, owner)
}

func (a *App) finishIssueRound(ctx context.Context, g issues.GitHub, root string, number int, owner issues.Owner) error {
	ledger, err := issues.Load(root)
	if err != nil {
		return err
	}
	record := ledger.Records[number]
	abandoned := record != nil && record.Status == "open" && record.Owner == (issues.Owner{}) && record.Branch == "" && record.Worktree == ""
	if record == nil || (record.Owner != owner && !abandoned) {
		return fmt.Errorf("issue #%d 的台账 owner 已改变，请核实后恢复", number)
	}
	// Refresh branch associations too: PR creation can succeed before the agent
	// receives its response or writes a URL to disk.
	snapshot, err := g.Snapshot(ctx, ledger)
	if err != nil {
		return err
	}
	ledger.Sync(snapshot)
	for _, issue := range snapshot {
		if issue.Number == number {
			for _, pr := range issue.PRs {
				if pr.Open() && pr.Head == record.Branch && pr.Base == "dev" {
					if err = a.store.attachPR(owner.Chat, pr.URL); err != nil {
						return err
					}
					log.Printf("issue #%d：PR 已确认并附加到本轮聊天 %s", number, pr.URL)
				}
			}
		}
	}
	if err = ledger.Save(); err != nil {
		return err
	}
	if record.Status == "processing" {
		return fmt.Errorf("issue #%d 仍为 processing，下一轮将从共享台账恢复。验证：%s；记录：%s", number, chatmodel.Clip(record.Validation, 2048), chatmodel.Clip(record.Notes, 2048))
	}
	return nil
}

func noteIssueFailure(root string, number int, owner issues.Owner, cause error) error {
	ledger, err := issues.Load(root)
	if err != nil {
		return err
	}
	record := ledger.Records[number]
	if record == nil || record.Owner != owner {
		return nil
	}
	if record.Status != "pr" && record.Status != "close" {
		record.Status = "processing"
	}
	record.History = append(record.History, time.Now().UTC().Format(time.RFC3339)+" 本轮未完成；在新聊天恢复："+chatmodel.Clip(cause.Error(), 4096))
	return ledger.Save()
}

func (a *App) automationArtifact(w http.ResponseWriter, r *http.Request) {
	if a.automation == nil || r.Header.Get("Origin") != "" {
		apiError(w, 403, "Forbidden")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a.automation.mu.Lock()
	scope := a.automation.scopes[token]
	a.automation.mu.Unlock()
	if scope == nil {
		apiError(w, 403, "Forbidden")
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.closed || scope.ctx.Err() != nil {
		apiError(w, 403, "Forbidden")
		return
	}
	var input struct {
		Type string `json:"artifact_type"`
		URL  string `json:"url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Type != "pull_request" || !strings.HasPrefix(input.URL, "https://github.com/"+scope.repository+"/pull/") {
		apiError(w, 400, "Invalid artifact")
		return
	}
	g := issues.GitHub{Run: a.automation.run, Directory: scope.directory, Repository: scope.repository}
	pr, err := g.PR(scope.ctx, input.URL)
	if err != nil {
		apiError(w, 502, err.Error())
		return
	}
	if !pr.Open() || pr.Head != scope.branch || pr.Base != "dev" {
		apiError(w, 400, "PR must be open with the claimed branch and dev base")
		return
	}
	if err = a.store.attachPR(scope.chat, pr.URL); err != nil {
		apiError(w, 500, "Could not attach PR")
		return
	}
	apiJSON(w, 200, map[string]bool{"ok": true})
}
