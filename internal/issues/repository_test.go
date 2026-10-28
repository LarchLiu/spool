package issues

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRepositoryReadsGitHubOrigin(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		remote string
		valid  bool
	}{
		{"https://github.com/Owner/repo", true},
		{"https://github.com/Owner/repo.git", true},
		{"https://github.com/Owner/repo.git/", true},
		{"git@github.com:Owner/repo.git", true},
		{"ssh://git@github.com/Owner/repo.git", true},
		{"https://gitlab.com/Owner/repo.git", false},
		{"git@github.com.evil:Owner/repo.git", false},
		{"https://github.com/Owner/repo/extra", false},
		{"https://github.com/Owner/repo?query", false},
		{"https://user:secret@github.com/Owner/repo.git", false},
		{"../repo.git", false},
		{"https://github.com/../repo", false},
		{"https://github.com/Owner/", false},
		{"", false},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			var calls []string
			repo, err := DetectRepository(context.Background(), func(_ context.Context, directory, name string, args ...string) ([]byte, error) {
				if directory != root || name != "git" {
					t.Fatalf("unexpected command directory=%q program=%q", directory, name)
				}
				call := strings.Join(args, " ")
				calls = append(calls, call)
				switch call {
				case "rev-parse --show-toplevel":
					return []byte(root + "\n"), nil
				case "remote get-url origin":
					return []byte(tc.remote + "\n"), nil
				default:
					return nil, fmt.Errorf("unexpected command: %s", call)
				}
			}, root)
			if tc.valid {
				if err != nil || repo != "Owner/repo" {
					t.Fatalf("repository=%q, error=%v", repo, err)
				}
			} else if err == nil || repo != "" {
				t.Fatalf("accepted unsupported origin: repository=%q, error=%v", repo, err)
			}
			if len(calls) != 2 {
				t.Fatalf("unexpected Git calls: %v", calls)
			}
		})
	}
}

func TestDetectRepositoryRejectsEmptyDirectoryWithoutInspectingCurrentProject(t *testing.T) {
	if _, err := DetectRepository(context.Background(), func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("read Git metadata from the application's current directory")
		return nil, nil
	}, ""); err == nil {
		t.Fatal("empty project directory accepted")
	}
}
