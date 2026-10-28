package workspace

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultDataDir isolates development data unless SPOOL_HOME explicitly overrides it.
func DefaultDataDir(development bool) string {
	if path := strings.TrimSpace(os.Getenv("SPOOL_HOME")); path != "" {
		return path
	}
	name := ".spool"
	if development {
		name = ".spool-dev"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), strings.TrimPrefix(name, "."))
	}
	return filepath.Join(home, name)
}

func databasePath(root string) string {
	return filepath.Join(root, "db", "spool.sqlite")
}

func prepareDatabase(root string) (string, error) {
	directory := filepath.Join(root, "db")
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("database directory must be a real directory, not a symlink: %s", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	// Serialize relocation across processes, including first startup after an update.
	guard, err := os.OpenFile(filepath.Join(directory, ".layout.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	if err := lockDatabaseLayout(guard); err != nil {
		return "", err
	}
	path := databasePath(root)
	legacy := filepath.Join(root, "spool.sqlite")
	currentExists, err := databaseExists(path)
	if err != nil {
		return "", err
	}
	legacyExists, err := databaseExists(legacy)
	if err != nil {
		return "", err
	}
	if currentExists && legacyExists {
		return "", fmt.Errorf("both %s and %s exist; stop all Spool instances and resolve the conflicting databases before starting", legacy, path)
	}
	for _, location := range []struct {
		path   string
		exists bool
	}{{path, currentExists}, {legacy, legacyExists}} {
		if !location.exists {
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if exists, err := databaseExists(location.path + suffix); err != nil {
					return "", err
				} else if exists {
					return "", fmt.Errorf("orphaned database sidecar %s; recover it before starting Spool", location.path+suffix)
				}
			}
		}
	}
	if legacyExists {
		if err := relocateDatabase(legacy, path); err != nil {
			return "", fmt.Errorf("move legacy database into db/: %w", err)
		}
	}
	return path, nil
}

func databaseExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("database must be a regular file, not a symlink: %s", path)
	}
	return true, nil
}

func relocateDatabase(source, destination string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := databaseExists(source + suffix); err != nil {
			return err
		}
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE; COMMIT;`); err != nil {
		return fmt.Errorf("cannot lock legacy database; stop all Spool instances before migrating: %w", err)
	}
	var hasLease int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='runtime_lease'`).Scan(&hasLease); err != nil {
		return err
	}
	if hasLease != 0 {
		var active int
		if err := db.QueryRow(`SELECT count(*) FROM runtime_lease WHERE expires_at>=?`, time.Now().UnixMilli()).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return errors.New("legacy database is still managed by another running instance; stop it first, or after a crash wait up to 30 seconds")
		}
	}
	// Let SQLite recover/checkpoint any crash WAL before moving the main file.
	// DELETE mode requires SQLite's exclusive lock and removes the WAL sidecars.
	var busy, pages, checkpointed int
	if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &pages, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("legacy database is busy; stop all Spool instances before migrating")
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return err
	}
	if mode != "delete" {
		return fmt.Errorf("legacy database is busy (journal mode %s); stop all Spool instances before migrating", mode)
	}
	if err := db.Close(); err != nil {
		return err
	}
	// Exclusive WAL recovery can leave an unused shared-memory index after a
	// crash. DELETE mode has already checkpointed all data and no longer uses it.
	if err := os.Remove(source + "-shm"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(source + suffix); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("legacy database sidecar %s still exists or cannot be checked; stop all Spool instances before migrating", suffix)
		}
	}
	return os.Rename(source, destination)
}
