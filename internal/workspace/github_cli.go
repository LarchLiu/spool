package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"spool/internal/issues"
)

func (s *Store) GitHubCLIPath() (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='github_cli_path'`).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return path, err
}

func (s *Store) SaveGitHubCLIPath(path string) error {
	path = strings.TrimSpace(path)
	if path != "" {
		if _, err := issues.ResolveGitHubCLI(path); err != nil {
			return errors.New("GitHub CLI path must point to an existing executable using an absolute path.")
		}
	}
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES('github_cli_path',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, path)
	return err
}

// RunIssueCommand reads the saved override at execution time so both desktop
// checks and an already-running headless/desktop scheduler use the same setting.
func (s *Store) RunIssueCommand(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	if name != "gh" {
		return issues.Run(ctx, directory, name, args...)
	}
	path, err := s.GitHubCLIPath()
	if err != nil {
		return nil, err
	}
	return issues.WithGitHubCLI(path)(ctx, directory, name, args...)
}

func (s *Store) issueGitHubCLIHint() (string, error) {
	path, err := s.GitHubCLIPath()
	if err != nil {
		return "", err
	}
	if path == "" {
		path, _ = issues.ResolveGitHubCLI("")
	}
	if path == "" {
		return "", nil
	}
	encoded, _ := json.Marshal(path)
	return "GitHub CLI executable (JSON string): " + string(encoded) + "\nUse this executable for every gh command, including API reads and PR creation. Quote it for the current shell (including the call operator in PowerShell); do not silently use another installation from PATH.\n\n", nil
}
