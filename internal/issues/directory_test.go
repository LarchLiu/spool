package issues

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestDirectoryEncodesEveryNonAlphanumericCharacter(t *testing.T) {
	for _, test := range []struct{ checkout, key string }{
		{"/Users/alex/Work/muyun/test", "-Users-alex-Work-muyun-test"},
		{"/work/a.b_c (test)/", "-work-a-b-c--test--"},
		{`C:\Work\test.app`, "C--Work-test-app"},
		{"/work/项目-1", "-work-项目-1"},
	} {
		if got, want := Directory("data", test.checkout), filepath.Join("data", "projects", test.key, "issues"); got != want {
			t.Errorf("Directory(%q) = %q, want %q", test.checkout, got, want)
		}
	}
}

func TestPrepareDirectoryConcurrentFirstUse(t *testing.T) {
	data := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "checkout")
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, err := PrepareDirectory(data, checkout); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestPrepareDirectoryIsPrivateAndRejectsPathCollisions(t *testing.T) {
	data := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "a.b")
	directory, err := PrepareDirectory(data, checkout)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PrepareDirectory(data, checkout); err != nil || got != directory {
		t.Fatalf("cannot reuse own directory: %q, %v", got, err)
	}
	for _, path := range []string{filepath.Join(data, "projects"), filepath.Dir(directory), directory} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("directory not created: %s, %v", path, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
			t.Fatalf("directory permissions = %v", info.Mode())
		}
	}
	collision := filepath.Join(filepath.Dir(checkout), "a-b")
	if _, err := PrepareDirectory(data, collision); err == nil {
		t.Fatal("different checkouts shared colliding issue state")
	}
	if _, err := PrepareDirectory(data, "relative/project"); err == nil {
		t.Fatal("accepted a relative checkout")
	}
}

func TestPrepareDirectoryRejectsSymlinkAtEveryProjectLevel(t *testing.T) {
	for _, level := range []string{"projects", "project", "issues", "identity"} {
		t.Run(level, func(t *testing.T) {
			data, elsewhere := t.TempDir(), t.TempDir()
			checkout := filepath.Join(t.TempDir(), "checkout")
			directory := Directory(data, checkout)
			var link string
			switch level {
			case "projects":
				link = filepath.Join(data, "projects")
			case "project":
				link = filepath.Dir(directory)
			case "issues":
				link = directory
			case "identity":
				link = filepath.Join(filepath.Dir(directory), ".project-directory")
				elsewhere = filepath.Join(elsewhere, "identity")
				if err := os.WriteFile(elsewhere, []byte(checkout), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, link); err != nil {
				t.Skip(err)
			}
			if _, err := PrepareDirectory(data, checkout); err == nil {
				t.Fatal("followed a symlink in the project state path")
			}
		})
	}
}
