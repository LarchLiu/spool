package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	// Mock native IDs must never cause tests to scan a developer's actual
	// provider history. Individual native-history tests can override these.
	root, err := os.MkdirTemp("", "spool-test-native-homes-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for variable, folder := range map[string]string{"CODEX_HOME": "codex", "CLAUDE_CONFIG_DIR": "claude"} {
		if err := os.Setenv(variable, filepath.Join(root, folder)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			_ = os.RemoveAll(root)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
