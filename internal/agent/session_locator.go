package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	chatmodel "spool/internal/model"
)

// FindSessionTranscript locates the provider-owned JSONL file for a native
// session ID. Provider files are deliberately discovered at read time: spool
// stores only the provider session ID and never copies the native transcript
// into SQLite.
func FindSessionTranscript(backend chatmodel.Backend, nativeID, workingDirectory string) (string, error) {
	if !backend.Valid() || strings.TrimSpace(nativeID) == "" {
		return "", errors.New("native session is not addressable")
	}
	if info, err := os.Stat(nativeID); err == nil && !info.IsDir() {
		return nativeID, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var roots []string
	switch backend {
	case chatmodel.BackendCodex:
		base := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if base == "" {
			base = filepath.Join(home, ".codex")
		}
		roots = []string{filepath.Join(base, "sessions")}
	case chatmodel.BackendClaude:
		base := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		if base == "" {
			base = filepath.Join(home, ".claude")
		}
		roots = []string{filepath.Join(base, "projects")}
	}
	for _, root := range roots {
		if path := findNativeFile(root, nativeID, backend, workingDirectory); path != "" {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func findNativeFile(root, nativeID string, backend chatmodel.Backend, workingDirectory string) string {
	if _, err := os.Stat(root); err != nil {
		return ""
	}
	// Both providers use the session ID as the basename. Check this cheap path
	// before walking, which is especially useful for Claude project folders.
	if backend == chatmodel.BackendClaude {
		encoded := encodeClaudeProjectPath(workingDirectory)
		candidate := filepath.Join(root, encoded, nativeID+".jsonl")
		if regularFile(candidate) {
			return candidate
		}
	} else {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "*"+nativeID+".jsonl"))
		for _, match := range matches {
			if regularFile(match) {
				return match
			}
		}
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || found != "" || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if strings.TrimSuffix(entry.Name(), ".jsonl") == nativeID || strings.Contains(entry.Name(), nativeID) {
			found = path
			return nil
		}
		if sessionFileMatches(path, nativeID) {
			found = path
		}
		return nil
	})
	return found
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sessionFileMatches(path, nativeID string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if stringValue(record["type"]) != "session_meta" && backendSessionID(record) == "" {
			continue
		}
		payload, _ := record["payload"].(map[string]any)
		for _, key := range []string{"session_id", "id"} {
			if stringValue(payload[key]) == nativeID || stringValue(record[key]) == nativeID {
				return true
			}
		}
	}
	return false
}

func backendSessionID(record map[string]any) string {
	if value := stringValue(record["sessionId"]); value != "" {
		return value
	}
	return stringValue(record["session_id"])
}

func encodeClaudeProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if encoded, err := claudeProjectName(filepath.Clean(path)); err == nil {
		return encoded
	}
	return ""
}
