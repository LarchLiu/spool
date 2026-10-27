package appupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type pendingUpdate struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Diagnostics holds bounded local update logs, not chat content or credentials.
// A pending restart is confirmed only after the new version's GUI is ready.
type Diagnostics struct {
	mu                   sync.Mutex
	Path                 string
	pendingPath, current string
	pending              *pendingUpdate
}

func OpenDiagnostics(root, current string) (*Diagnostics, error) {
	d := &Diagnostics{Path: filepath.Join(root, "update.log"), pendingPath: filepath.Join(root, "update-pending.json"), current: current}
	data, err := os.ReadFile(d.pendingPath)
	if err == nil {
		var pending pendingUpdate
		if err = json.Unmarshal(data, &pending); err != nil || !versionPattern.MatchString(pending.To) {
			if err = d.Record("restart_record_invalid", current, errors.New("invalid pending restart record")); err != nil {
				return nil, err
			}
		} else {
			d.pending = &pending
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err = d.Record("startup_started", current, nil); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Diagnostics) Record(phase, version string, cause error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.record(phase, version, cause)
}

func (d *Diagnostics) record(phase, version string, cause error) error {
	if info, err := os.Stat(d.Path); err == nil && info.Size() >= 256<<10 {
		if err := os.Remove(d.Path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(d.Path, d.Path+".1"); err != nil {
			return err
		}
	}
	entry := struct {
		Time    time.Time `json:"time"`
		Phase   string    `json:"phase"`
		Version string    `json:"version,omitempty"`
		Error   string    `json:"error,omitempty"`
	}{time.Now().UTC(), phase, version, SafeError(cause)}
	if len(entry.Error) > 2048 {
		entry.Error = entry.Error[:2048]
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(d.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (d *Diagnostics) RequestRestart(version string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !versionPattern.MatchString(version) {
		return errors.New("invalid pending update version")
	}
	pending := &pendingUpdate{From: d.current, To: version}
	data, _ := json.Marshal(pending)
	file, err := os.CreateTemp(filepath.Dir(d.pendingPath), ".update-pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), d.pendingPath); err != nil {
		return err
	}
	d.pending = pending
	return d.record("restart_requested", version, nil)
}

func (d *Diagnostics) Ready() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.record("startup_ready", d.current, nil); err != nil {
		return err
	}
	if d.pending == nil {
		return nil
	}
	if d.current == d.pending.To {
		if err := d.record("restart_confirmed", d.current, nil); err != nil {
			return err
		}
		if err := os.Remove(d.pendingPath); err != nil {
			return err
		}
		d.pending = nil
		return nil
	}
	err := fmt.Errorf("restart into %s is unconfirmed; running %s", d.pending.To, d.current)
	if writeErr := d.record("restart_unconfirmed", d.current, err); writeErr != nil {
		return writeErr
	}
	return err
}
