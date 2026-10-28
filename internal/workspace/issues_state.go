package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

func (a *App) issueOwnerEnded(owner issues.Owner) bool {
	a.turnMu.Lock()
	active := a.activeTurns[owner.Chat] != nil
	a.turnMu.Unlock()
	return !active && owner.Ended(a.automation.instance, a.issueRunEnded)
}

func (a *App) acquireIssueState(ctx context.Context, directory string, owner issues.Owner) (*issues.Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lock, err := issues.Acquire(directory, owner, a.issueOwnerEnded)
		if !errors.Is(err, issues.ErrLocked) {
			return lock, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func mergeIssueTask(ledger *issues.Ledger, number int, owner issues.Owner) error {
	directory, err := issues.TaskDirectory(filepath.Dir(ledger.Path), owner.Run)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(directory, "issues.json")); os.IsNotExist(err) {
		// Older claims already have their progress in the shared ledger.
		return nil
	} else if err != nil {
		return err
	}
	task, err := issues.Load(directory)
	if err != nil {
		return err
	}
	return ledger.MergeTask(task, number, owner)
}

// Import crash-recovery files only after proving the executor has ended. A bad
// task file blocks that issue, not unrelated labels or the shared JSON document.
func (a *App) recoverIssueTasks(ledger *issues.Ledger) map[int]error {
	blocked := map[int]error{}
	for number, record := range ledger.Records {
		if record.Status != "processing" || record.Owner.Automation != "issues" || !a.issueOwnerEnded(record.Owner) {
			continue
		}
		if err := mergeIssueTask(ledger, number, record.Owner); err != nil {
			blocked[number] = fmt.Errorf("issue #%d 的任务进度无法导入：%w", number, err)
		}
	}
	return blocked
}

// State transactions serialize this cache. Fetch the project-wide snapshot only
// once per timer check, not once per configured label. Later workers use the
// already-synchronized ledger, preserving any newer sibling finalization.
func (a *App) issueBatchSnapshot(ctx context.Context, g issues.GitHub, ledger *issues.Ledger, batch *issueBatch) ([]issues.Issue, map[int]error, bool, error) {
	if batch != nil {
		batch.mu.Lock()
		defer batch.mu.Unlock()
		if batch.loaded {
			if !batch.synced {
				a.recoverIssueTasks(ledger)
			}
			return batch.snapshot, batch.blocked, batch.synced, batch.err
		}
	}
	blocked := a.recoverIssueTasks(ledger)
	snapshot, err := g.Snapshot(ctx, ledger)
	if batch != nil {
		batch.loaded, batch.snapshot, batch.blocked, batch.err = true, snapshot, blocked, err
	}
	return snapshot, blocked, false, err
}

func saveIssueBatch(ledger *issues.Ledger, batch *issueBatch) error {
	if err := ledger.Save(); err != nil {
		return err
	}
	if batch != nil {
		batch.mu.Lock()
		batch.synced = true
		batch.mu.Unlock()
	}
	return nil
}

func (a *App) finishIssueTask(ctx context.Context, g issues.GitHub, directory string, number int, owner issues.Owner, labels []string, cause error) (resultErr error) {
	// Cancellation must still import local progress before releasing the labels.
	cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lock, err := a.acquireIssueState(cleanup, directory, owner)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Release()) }()
	ledger, err := issues.Load(directory)
	if err != nil {
		return err
	}
	record := ledger.Records[number]
	if record == nil || record.Owner != owner {
		return fmt.Errorf("issue #%d 的台账 owner 已改变，请核实后恢复", number)
	}
	if err = mergeIssueTask(ledger, number, owner); err != nil {
		return err
	}
	if cause != nil {
		record.Status, record.Owner = "processing", owner
		record.History = append(record.History, time.Now().UTC().Format(time.RFC3339)+" 本轮未完成；在新聊天恢复："+chatmodel.Clip(cause.Error(), 4096))
	} else if ctx.Err() == nil {
		var snapshot []issues.Issue
		snapshot, resultErr = g.Snapshot(ctx, ledger)
		if resultErr == nil {
			ledger.Sync(snapshot, labels)
		}
	}
	if err = ledger.Save(); err != nil {
		return errors.Join(resultErr, err)
	}
	if cause == nil && record.Status == "processing" && resultErr == nil {
		resultErr = fmt.Errorf("issue #%d 仍为 processing，下一轮将从共享台账恢复。验证：%s；记录：%s", number, chatmodel.Clip(record.Validation, 2048), chatmodel.Clip(record.Notes, 2048))
	}
	return resultErr
}
