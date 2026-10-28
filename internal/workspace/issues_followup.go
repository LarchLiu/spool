package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

// Human follow-ups on task chats participate in the same project lock. A
// scheduled recovery must never overlap an assignee continuing the old chat.
func (a *App) startIssueFollowup(chat chatmodel.Session, input messageInput, options chatmodel.CodexOptions) error {
	if a.automation == nil {
		return fmt.Errorf("issue scheduler is unavailable")
	}
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		return err
	}
	a.turnMu.Lock()
	if a.closing || a.updating {
		a.turnMu.Unlock()
		return fmt.Errorf("agent service is stopping or updating")
	}
	a.runningTurns++
	a.turns.Add(1)
	a.turnMu.Unlock()
	var admission sync.Once
	releaseAdmission := func() { admission.Do(func() { a.turnMu.Lock(); a.runningTurns--; a.turnMu.Unlock(); a.turns.Done() }) }
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-a.shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	handedOff := false
	defer func() {
		if !handedOff {
			cancel()
			releaseAdmission()
		}
	}()
	config, err := a.store.IssuesAutomation(project.ID)
	if err != nil {
		return err
	}
	checkout, err := issues.Inspect(ctx, a.automation.run, project.Path, config.Repository)
	if err != nil {
		return err
	}
	config.Repository = checkout.Repository
	if err = ctx.Err(); err != nil {
		return err
	}
	owner := issues.NewOwner(a.automation.instance, chatmodel.NewID())
	owner.Chat = chat.ID
	lock, err := issues.Acquire(checkout.Root, owner, func(o issues.Owner) bool {
		a.turnMu.Lock()
		active := a.activeTurns[o.Chat] != nil
		a.turnMu.Unlock()
		return !active && o.Ended(a.automation.instance, a.issueRunEnded)
	})
	if err != nil {
		return err
	}
	if _, err = a.store.db.Exec(`INSERT INTO automation_runs(id,project_id,instance,chat_id,status,started_at) VALUES(?,?,?,?,'running',?)`, owner.Run, project.ID, owner.Instance, chat.ID, time.Now().UnixMilli()); err != nil {
		return errors.Join(err, lock.Release())
	}
	fail := func(cause error) error {
		_, e := a.store.db.Exec(`UPDATE automation_runs SET status='failed',finished_at=?,error=? WHERE id=?`, time.Now().UnixMilli(), chatmodel.Clip(cause.Error(), 8192), owner.Run)
		return errors.Join(cause, e, lock.Release())
	}
	ledger, err := issues.Load(checkout.Root)
	if err != nil {
		return fail(err)
	}
	var record *issues.Record
	for _, r := range ledger.Records {
		if r.Owner.Automation == "issues" && r.Owner.Chat == chat.ID {
			record = r
			break
		}
	}
	token := ""
	if record != nil && record.Branch != "" {
		record.Owner = owner
		record.History = append(record.History, time.Now().UTC().Format(time.RFC3339)+" 负责人主动继续任务聊天，run "+owner.Run)
		if err = ledger.Save(); err != nil {
			return fail(err)
		}
		token = randomToken()
		a.automation.mu.Lock()
		a.automation.scopes[token] = &issueArtifactScope{chat: chat.ID, branch: record.Branch, repository: config.Repository, directory: project.Path, ctx: ctx}
		a.automation.mu.Unlock()
		payload, _ := json.Marshal(issueRecordPreview(*record))
		input.Message = "这是负责人主动发送的后续消息，不是定时唤醒。后端持有项目排他锁直到本次执行结束，不要释放锁。继续遵守 4–8 点与唯一共享台账约定，使用主 checkout 的 " + ledger.Path + "。此前附件凭据已失效；仅使用本消息中的新 attach_artifact 函数（不要泄露凭据）。本次当前记录：\n" + string(payload) + "\n" + issueArtifactFunction(a.automation.callbackURL, token) + "\n负责人消息：\n" + input.Message
	} else {
		input.Message = "这是负责人主动发送的消息，后端持有项目排他锁。此聊天不再拥有共享台账中的当前认领，可能已由下一轮新聊天接手。可以讨论和检查，但不得沿用过时上下文修改代码或台账、push 或创建 PR；如需继续处理请核实并转到台账 owner.chat 对应的新聊天。\n\n负责人消息：\n" + input.Message
	}
	cleanupScope := func() {
		cancel()
		if token != "" {
			a.automation.mu.Lock()
			scope := a.automation.scopes[token]
			delete(a.automation.scopes, token)
			a.automation.mu.Unlock()
			if scope != nil {
				scope.mu.Lock()
				scope.closed = true
				scope.mu.Unlock()
			}
		}
	}
	finish := func(runErr error) {
		defer releaseAdmission()
		if record != nil {
			select {
			case <-a.shutdown:
			default:
				if runErr == nil {
					runErr = a.finishIssueRound(ctx, issues.GitHub{Run: a.automation.run, Directory: project.Path, Repository: config.Repository}, checkout.Root, record.Number, owner)
				}
			}
		}
		status, message := "finished", ""
		if runErr != nil {
			status = "failed"
			message = chatmodel.Clip(runErr.Error(), 8192)
			log.Printf("任务聊天 %s 未完成：%s", chat.ID, message)
		}
		cleanupScope()
		_, saveErr := a.store.db.Exec(`UPDATE automation_runs SET status=?,finished_at=?,error=? WHERE id=?`, status, time.Now().UnixMilli(), message, owner.Run)
		_, statusErr := a.store.db.Exec(`UPDATE project_automations SET last_error=? WHERE project_id=?`, message, project.ID)
		if err := errors.Join(saveErr, statusErr, lock.Release()); err != nil {
			log.Printf("任务聊天 %s 释放项目锁失败：%v", chat.ID, err)
		}
	}
	if err = a.startTurnWithCallback(chat, input, options, finish); err != nil {
		finish(err)
		return err
	}
	handedOff = true
	return nil
}
