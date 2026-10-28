package issues

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrLocked = errors.New("issue automation is already running or its owner cannot be proven finished")
var ErrLockOwnerUnknown = errors.New("项目 issues 锁的原 owner 无法确认已结束；已保留锁和现场，请核实 owner.json 后重试，不能仅按时间清锁")

type Owner struct {
	Automation string `json:"automation"`
	Run        string `json:"run"`
	Chat       string `json:"chat,omitempty"`
	Instance   string `json:"instance"`
	Host       string `json:"host"`
	PID        int    `json:"pid"`
}

func NewOwner(instance, run string) Owner {
	host, _ := os.Hostname()
	return Owner{Automation: "issues", Run: run, Instance: instance, Host: host, PID: os.Getpid()}
}

// Ended never treats age as evidence. Unknown hosts and reused PIDs stay locked.
func (o Owner) Ended(instance string, runEnded func(string) bool) bool {
	if o.Run == "" || o.Instance == "" || o.Host == "" || o.PID <= 0 {
		return false
	}
	host, _ := os.Hostname()
	if o.Host != host {
		return false
	}
	if o.Instance == instance && o.PID == os.Getpid() && runEnded != nil && runEnded(o.Run) {
		return true
	}
	return processEnded(o.PID)
}

type Lock struct {
	Path  string
	Owner Owner
	guard *os.File
}

func Acquire(directory string, owner Owner, ended func(Owner) bool) (*Lock, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("automation state directory must not be a symlink")
	}
	guardPath := filepath.Join(directory, ".issues-automation.guard")
	if info, e := os.Lstat(guardPath); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("automation guard must be a regular file")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	// The advisory guard serializes stale-directory reclamation as well as mkdir.
	guard, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(guard); err != nil {
		guard.Close()
		return nil, err
	}
	l := &Lock{Path: filepath.Join(directory, ".issues-automation.lock"), Owner: owner, guard: guard}
	fail := func(err error) (*Lock, error) { unlockFile(guard); guard.Close(); return nil, err }
	if err = os.Mkdir(l.Path, 0700); errors.Is(err, os.ErrExist) {
		if info, e := os.Lstat(l.Path); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail(ErrLocked)
		}
		var previous Owner
		body, readErr := os.ReadFile(filepath.Join(l.Path, "owner.json"))
		if readErr != nil || json.Unmarshal(body, &previous) != nil || previous.Automation != owner.Automation || ended == nil || !ended(previous) {
			return fail(ErrLockOwnerUnknown)
		}
		if err = os.Remove(filepath.Join(l.Path, "owner.json")); err != nil {
			return fail(err)
		}
		if err = os.Remove(l.Path); err != nil {
			return fail(err)
		}
		err = os.Mkdir(l.Path, 0700)
	}
	if err != nil {
		return fail(err)
	}
	if err = l.SetOwner(owner); err != nil {
		_ = os.Remove(l.Path)
		return fail(err)
	}
	return l, nil
}

func (l *Lock) SetOwner(owner Owner) error {
	if owner.Run != l.Owner.Run || owner.Instance != l.Owner.Instance {
		return fmt.Errorf("lock ownership cannot be transferred")
	}
	body, err := json.MarshalIndent(owner, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(l.Path, "owner.json"), body, 0600)
	}
	if err == nil {
		l.Owner = owner
	}
	return err
}

func (l *Lock) Release() error {
	defer l.guard.Close()
	defer unlockFile(l.guard)
	body, err := os.ReadFile(filepath.Join(l.Path, "owner.json"))
	var owner Owner
	if err != nil || json.Unmarshal(body, &owner) != nil || owner != l.Owner {
		return fmt.Errorf("lock owner changed; refusing to remove it")
	}
	if err = os.Remove(filepath.Join(l.Path, "owner.json")); err != nil {
		return err
	}
	return os.Remove(l.Path)
}
