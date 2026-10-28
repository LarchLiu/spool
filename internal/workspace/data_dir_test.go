package workspace

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDatabaseLivesInPrivateSubdirectory(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := store.DatabasePath(), filepath.Join(store.Root(), "db", "spool.sqlite"); got != want {
		t.Fatalf("DatabasePath() = %q, want %q", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spool.sqlite", "spool.sqlite-wal", "spool.sqlite-shm"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("database file escaped db/: %s, %v", name, err)
		}
	}
	if runtime.GOOS != "windows" {
		for name, want := range map[string]os.FileMode{"db": 0o700, "db/spool.sqlite": 0o600} {
			info, err := os.Stat(filepath.Join(root, name))
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("private permissions missing for %s: %v, %v", name, info, err)
			}
		}
	}
}

func seedLegacyDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE migration_marker(value TEXT); INSERT INTO migration_marker VALUES('preserved');`); err != nil {
		t.Fatal(err)
	}
}

func assertDatabaseMarker(t *testing.T, store *Store) {
	t.Helper()
	var value string
	if err := store.db.QueryRow(`SELECT value FROM migration_marker`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("legacy data lost: %q, %v", value, err)
	}
}

func TestLegacyDatabaseRelocatesAndReopens(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "spool.sqlite")
	seedLegacyDatabase(t, legacy)
	for range 2 {
		store, err := OpenStore(root)
		if err != nil {
			t.Fatal(err)
		}
		assertDatabaseMarker(t, store)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy database was not relocated: %v", err)
	}
}

func TestLegacyDatabaseWithCrashWALRelocatesWithoutLosingWrites(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "spool.sqlite")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestLegacyDatabaseWALWriter$")
	cmd.Env = append(os.Environ(), "SPOOL_TEST_LEGACY_DATABASE="+legacy)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed crash WAL: %s, %v", output, err)
	}
	if info, err := os.Stat(legacy + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("fixture did not leave uncheckpointed WAL: %v, %v", info, err)
	}
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertDatabaseMarker(t, store)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(legacy + suffix); !os.IsNotExist(err) {
			t.Fatalf("legacy file left behind: %s, %v", suffix, err)
		}
	}
}

func TestLegacyDatabaseWALWriter(t *testing.T) {
	path := os.Getenv("SPOOL_TEST_LEGACY_DATABASE")
	if path == "" {
		return
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
CREATE TABLE migration_marker(value TEXT); INSERT INTO migration_marker VALUES('preserved');`); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: do not close SQLite or run test cleanup/checkpointing.
	os.Exit(0)
}

func TestLegacyDatabaseLeasePreventsRelocation(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "active"
		expires := time.Now().Add(time.Hour).UnixMilli()
		if expired {
			name = "expired"
			expires = time.Now().Add(-time.Hour).UnixMilli()
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			legacy := filepath.Join(root, "spool.sqlite")
			seedLegacyDatabase(t, legacy)
			db, err := sql.Open("sqlite", legacy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE runtime_lease(id INTEGER PRIMARY KEY,token TEXT,expires_at INTEGER); INSERT INTO runtime_lease VALUES(1,'old-instance',?)`, expires); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(root)
			if expired {
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				assertDatabaseMarker(t, store)
				return
			}
			if store != nil || err == nil || !strings.Contains(err.Error(), "another running instance") {
				t.Fatalf("active legacy database was not rejected: %v, %v", store, err)
			}
			if _, err := os.Stat(legacy); err != nil {
				t.Fatalf("active legacy database was moved: %v", err)
			}
			if _, err := os.Stat(databasePath(root)); !os.IsNotExist(err) {
				t.Fatalf("a second database was created despite active legacy instance: %v", err)
			}
		})
	}
}

func TestConflictingDatabaseLocationsAreNotOverwritten(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, "spool.sqlite")
	seedLegacyDatabase(t, legacy)
	paths := []string{legacy, databasePath(root)}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if store, err := OpenStore(root); store != nil || err == nil || !strings.Contains(err.Error(), "conflicting databases") {
		t.Fatalf("conflicting databases were not rejected: %v, %v", store, err)
	}
	for i, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before[i], after) {
			t.Fatalf("conflict modified %s: %v", path, err)
		}
	}
}

func TestConcurrentLegacyRelocation(t *testing.T) {
	root := t.TempDir()
	seedLegacyDatabase(t, filepath.Join(root, "spool.sqlite"))
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			path, err := prepareDatabase(root)
			if err != nil || path != databasePath(root) {
				t.Errorf("concurrent migration: %q, %v", path, err)
			}
		})
	}
	workers.Wait()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertDatabaseMarker(t, store)
}

func TestOrphanedDatabaseSidecarsPreventNewDatabase(t *testing.T) {
	for _, name := range []string{"spool.sqlite-wal", "spool.sqlite-shm", "db/spool.sqlite-wal", "db/spool.sqlite-shm"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("unrecovered"), 0o600); err != nil {
				t.Fatal(err)
			}
			if store, err := OpenStore(root); store != nil || err == nil || !strings.Contains(err.Error(), "orphaned") {
				t.Fatalf("orphaned sidecar was not rejected: %v, %v", store, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "unrecovered" {
				t.Fatalf("orphaned sidecar changed: %q, %v", data, err)
			}
			if _, err := os.Stat(databasePath(root)); !os.IsNotExist(err) {
				t.Fatalf("a new database was created over recovery data: %v", err)
			}
		})
	}
}

func TestDatabaseLayoutRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require Windows developer privileges")
	}
	for _, name := range []string{"db", "spool.sqlite", "db/spool.sqlite"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			if name != "db" {
				outside = filepath.Join(outside, "spool.sqlite")
				seedLegacyDatabase(t, outside)
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
			if store, err := OpenStore(root); store != nil || err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("database layout followed a symlink: %v, %v", store, err)
			}
		})
	}
}
