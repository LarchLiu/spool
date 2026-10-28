package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

// Follow-ups atomically reserve their labels too. A scheduled recovery cannot
// overlap an assignee continuing the old chat or reclaim a still-running turn.
func (a *App) startIssueFollowup(chat chatmodel.Session, input messageInput, options chatmodel.CodexOptions) error {
	if a.automation == nil {
		return fmt.Errorf("issue scheduler is unavailable")
	}
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		return err
	}
	cliHint, err := a.store.issueGitHubCLIHint()
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
			_ = lock.Release()
		}
	}()

	if _, err = a.store.db.Exec(`INSERT INTO automation_runs(id,project_id,instance,chat_id,status,started_at) VALUES(?,?,?,?,'running',?)`, owner.Run, project.ID, owner.Instance, chat.ID, time.Now().UnixMilli()); err != nil {
		return err
	}
	fail := func(cause error) error {
		_, e := a.store.db.Exec(`UPDATE automation_runs SET status='failed',finished_at=?,error=? WHERE id=?`, time.Now().UnixMilli(), chatmodel.Clip(cause.Error(), 8192), owner.Run)
		return errors.Join(cause, e)
	}
	ledger, err := issues.Load(directory)
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
	var task *issues.Ledger
	if record != nil && record.Branch != "" {
		if !a.issueOwnerEnded(record.Owner) {
			return fail(issues.ErrLocked)
		}
		// Labels/settings may have changed since this chat's previous turn.
		github := issues.GitHub{Run: a.automation.run, Directory: project.Path, Repository: config.Repository}
		snapshot, e := github.Snapshot(ctx, ledger)
		if e != nil {
			return fail(e)
		}
		ledger.Sync(snapshot, config.IssueLabels())
		for _, issue := range snapshot {
			if issue.Number == record.Number {
				if len(config.IssueLabels()) == 0 || len(record.Categories) == 0 {
					record.Categories = issues.Categories(issue, config.IssueLabels())
				}
				break
			}
		}
		if !ledger.CanClaim(record, a.issueOwnerEnded) {
			return fail(issues.ErrLocked)
		}
		if err = mergeIssueTask(ledger, record.Number, record.Owner); err != nil {
			return fail(err)
		}
		record.Owner = owner
		record.Status = "processing"
		record.History = append(record.History, time.Now().UTC().Format(time.RFC3339)+" 负责人主动继续任务聊天，run "+owner.Run)
		task, err = issues.PrepareTask(directory, *record)
		if err != nil {
			return fail(err)
		}
		if err = ledger.Save(); err != nil {
			return fail(err)
		}
		input.writableDirectories = []string{filepath.Dir(task.Path)}
		payload, _ := json.Marshal(issueRecordPreview(*record))
		input.Message = "这是负责人主动发送的后续消息，不是定时唤醒。后端已认领本条 issue 的全部匹配 labels；其他不相交 label 可并发。categories 为空数组表示占用整个项目的唯一名额，不按 issue labels 过滤。唯一共享台账 " + ledger.Path + " 仅由 Go 写入。你只能修改本轮独立 task_ledger：" + task.Path + "，其中 records 数组只能包含当前一条记录。Go 在你真正退出后校验并合并进度；不得改共享台账、其他任务或任何锁。继续遵守 4–8 点；只在本 issue 独立 worktree 编辑，不在共享主 checkout 操作。notes、validation 是字符串（只写简短验证摘要、阻塞和下一步，不堆积完整日志，不要改成数组或对象）；history、prs、files、categories 是字符串数组；owner、number、categories 保持原值。创建或复用 PR 后将完整 URL 写入本条记录的 prs 数组并在回复展示，由 Go 独立核验。不需要调用聊天附件接口，此前的附件函数与凭据不再使用。本次当前记录：\n" + string(payload) + "\n负责人消息：\n" + input.Message
	} else {
		record = nil
		input.Message = "这是负责人主动发送的消息。此聊天不再拥有共享台账中的当前认领，可能已由下一轮新聊天接手。可以讨论和检查，但不得沿用过时上下文修改代码或台账、push 或创建 PR；如需继续处理请核实并转到台账 owner.chat 对应的新聊天。\n\n负责人消息：\n" + input.Message
	}
	if err = lock.Release(); err != nil {
		lock = nil
		return fail(err)
	}
	lock = nil
	var execution *issues.Lock
	if task != nil {
		execution, err = issues.Acquire(filepath.Dir(task.Path), owner, a.issueOwnerEnded)
		if err != nil {
			return fail(err)
		}
	}

	input.Message = cliHint + input.Message
	finish := func(runErr error) {
		defer releaseAdmission()
		if record != nil {
			runErr = errors.Join(runErr, a.finishIssueTask(ctx, issues.GitHub{Run: a.automation.run, Directory: project.Path, Repository: config.Repository}, directory, record.Number, owner, config.IssueLabels(), runErr))
		}
		if execution != nil {
			runErr = errors.Join(runErr, execution.Release())
		}

		status, message := "finished", ""
		if runErr != nil {
			status = "failed"
			message = chatmodel.Clip(runErr.Error(), 8192)
			log.Printf("任务聊天 %s 未完成：%s", chat.ID, message)
		}
		cancel()
		_, saveErr := a.store.db.Exec(`UPDATE automation_runs SET status=?,finished_at=?,error=? WHERE id=?`, status, time.Now().UnixMilli(), message, owner.Run)
		_, statusErr := a.store.db.Exec(`UPDATE project_automations SET last_error=? WHERE project_id=?`, message, project.ID)
		if err := errors.Join(saveErr, statusErr); err != nil {
			log.Printf("任务聊天 %s 保存执行结果失败：%v", chat.ID, err)
		}
	}
	if err = a.startTurnWithCallback(chat, input, options, finish); err != nil {
		finish(err)
		return err
	}
	handedOff = true
	return nil
}
