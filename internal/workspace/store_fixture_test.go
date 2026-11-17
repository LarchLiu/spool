package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Keep only the closed, checkpointed empty database in memory. Generating it
// through OpenStore picks up schema changes without a checked-in snapshot.
var testStoreSchema = sync.OnceValues(func() (data []byte, err error) {
	root, err := os.MkdirTemp("", "spool-test-store-template-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(root)) }()
	store, err := OpenStore(root)
	if err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(store.DatabasePath())
})

// openTestStore seeds a private database, then retains the production connection
// setup and migration checks. First-start and migration tests use OpenStore.
func openTestStore(root string) (*Store, error) {
	data, err := testStoreSchema()
	if err != nil {
		return nil, fmt.Errorf("prepare test database template: %w", err)
	}
	path := databasePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return nil, err
	}
	return OpenStore(root)
}

func TestStoreFixtureCopiesKeepIndependentDataAndSchema(t *testing.T) {
	t.Parallel()
	first := testStore(t)
	second := testStore(t)
	testUser(t, first, "only-in-first")
	if _, err := first.db.Exec(`CREATE TABLE fixture_private(value TEXT); INSERT INTO fixture_private VALUES('private')`); err != nil {
		t.Fatal(err)
	}
	assertEmpty := func(store *Store) {
		t.Helper()
		var users, privateTables int
		if err := store.db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
			t.Fatalf("fixture inherited users: count=%d err=%v", users, err)
		}
		if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='fixture_private'`).Scan(&privateTables); err != nil || privateTables != 0 {
			t.Fatalf("fixture inherited private schema: count=%d err=%v", privateTables, err)
		}
		if _, err := store.db.Exec(`INSERT INTO user_avatars(user_id,style,seed) VALUES('missing-user','clay','')`); err == nil {
			t.Fatal("fixture connection did not enforce foreign keys")
		}
	}
	assertEmpty(second)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(first.Root()); err != nil {
		t.Fatal(err)
	}
	// Deleting one fixture must affect neither an existing copy nor the template
	// used by future tests, including after the template's first caller exits.
	assertEmpty(second)
	assertEmpty(testStore(t))
}
