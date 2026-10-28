package issues

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskMergePreservesOtherRecordsAndBackendOwnedFields(t *testing.T) {
	directory := t.TempDir()
	l, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewOwner("instance", "run-1")
	r := &Record{Number: 1, Title: "original", Status: "processing", Owner: owner, Categories: []string{"one"}, Notes: "human note", Extra: map[string]json.RawMessage{"custom": json.RawMessage(`true`)}}
	l.Records[1] = r
	l.Records[2] = &Record{Number: 2, Status: "processing", Owner: NewOwner("instance", "run-2"), Notes: "other task"}
	task, err := PrepareTask(directory, *r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareTask(directory, *r); err == nil {
		t.Fatal("reused existing task file")
	}
	update := task.Records[1]
	update.Title, update.Status, update.Categories = "stale title", "pr", []string{"changed"}
	update.Notes, update.Validation = "short task summary", "go test passed"
	update.Branch, update.Worktree = "codex/issue-1", "/isolated/1"
	update.PRs = []string{"https://github.com/o/r/pull/8"}
	update.History = []string{"verified"}
	r.Title, r.Categories = "new remote title", []string{"one", "two"}
	if err := l.MergeTask(task, 1, owner); err != nil {
		t.Fatal(err)
	}
	if r.Title != "new remote title" || len(r.Categories) != 2 || r.Owner != owner || r.Status != "processing" || r.Notes != "human note\nshort task summary" || r.Validation != "go test passed" || len(r.PRs) != 1 || string(r.Extra["custom"]) != "true" || l.Records[2].Notes != "other task" {
		t.Fatalf("merge corrupted shared state: %#v", r)
	}
	if err := l.MergeTask(task, 1, owner); err != nil || len(r.History) != 1 || len(r.PRs) != 1 || r.Notes != "human note\nshort task summary" {
		t.Fatal("recovery merge was not idempotent")
	}
	update.Owner.Chat = "foreign"
	if err := l.MergeTask(task, 1, owner); err == nil {
		t.Fatal("accepted changed owner")
	}
}

func TestTaskDirectoryRejectsTraversalAndSymlinkParents(t *testing.T) {
	root := t.TempDir()
	for _, run := range []string{"", ".", "..", "../outside", `..\outside`} {
		if _, err := TaskDirectory(root, run); err == nil {
			t.Fatalf("accepted run %q", run)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "runs")); err != nil {
		t.Skip(err)
	}
	if _, err := PrepareTask(root, Record{Number: 1, Status: "processing", Owner: NewOwner("instance", "run")}); err == nil {
		t.Fatal("followed a symlink run parent")
	}
}
