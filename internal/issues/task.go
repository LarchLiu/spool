package issues

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func TaskDirectory(directory, run string) (string, error) {
	if run == "" || run == "." || run == ".." || strings.ContainsAny(run, "/\\\x00") {
		return "", fmt.Errorf("invalid issue run identifier")
	}
	path := filepath.Join(directory, "runs", run)
	for _, parent := range []string{filepath.Dir(path), path} {
		if info, err := os.Lstat(parent); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", fmt.Errorf("issue run directory must not be a symlink")
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return path, nil
}

// Each executor can write only its own record. Only Go writes the shared ledger.
func PrepareTask(directory string, record Record) (*Ledger, error) {
	path, err := TaskDirectory(directory, record.Owner.Run)
	if err != nil {
		return nil, err
	}
	for _, parent := range []string{filepath.Dir(path), path} {
		if err := privateDirectory(parent); err != nil {
			return nil, err
		}
	}
	l, err := Load(path)
	if err != nil {
		return nil, err
	}
	if l.original != nil {
		return nil, fmt.Errorf("issue run already has a task ledger")
	}
	body, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var copy Record
	if err = json.Unmarshal(body, &copy); err != nil {
		return nil, err
	}
	l.Records[record.Number] = &copy
	return l, l.Save()
}

func (l *Ledger) MergeTask(task *Ledger, number int, owner Owner) error {
	r := l.Records[number]
	update := task.Records[number]
	if r == nil || r.Owner != owner || len(task.Records) != 1 || update == nil {
		return fmt.Errorf("issue #%d task record or shared owner changed", number)
	}
	abandoned := update.Status == "open" && update.Owner == (Owner{}) && update.Branch == "" && update.Worktree == ""
	if update.Owner != owner && !abandoned {
		return fmt.Errorf("issue #%d task changed its owner", number)
	}
	for _, pr := range update.PRs {
		if !ValidPRURL(pr) {
			return fmt.Errorf("invalid task PR URL")
		}
	}
	// Remote metadata, occupied labels and execution identity stay backend-owned.
	r.Branch, r.Worktree, r.Files = update.Branch, update.Worktree, update.Files
	r.Notes = mergeSummary(r.Notes, update.Notes)
	r.Validation = mergeSummary(r.Validation, update.Validation)
	for _, line := range update.History {
		if !contains(r.History, line) {
			r.History = append(r.History, line)
		}
	}
	for _, pr := range update.PRs {
		if !contains(r.PRs, pr) {
			r.PRs = append(r.PRs, pr)
		}
	}
	if r.Extra == nil {
		r.Extra = map[string]json.RawMessage{}
	}
	for key, value := range update.Extra {
		if _, exists := r.Extra[key]; !exists {
			r.Extra[key] = value
		}
	}
	if abandoned {
		r.Status, r.Owner = "open", Owner{}
	} else {
		// An agent's pr/close value is not evidence; GitHub must verify it again.
		r.Status = "processing"
	}
	return nil
}

func mergeSummary(current, proposed string) string {
	if proposed == "" || strings.Contains(current, proposed) {
		return current
	}
	if current == "" || strings.HasPrefix(proposed, current) {
		return proposed
	}
	return current + "\n" + proposed
}
