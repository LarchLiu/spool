package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	chatmodel "spool/internal/model"
)

type nativeSessionTitle struct {
	text     string
	priority int
}

func (t *nativeSessionTitle) collect(backend chatmodel.Backend, record map[string]any) {
	if t == nil {
		return
	}
	text, priority := "", 0
	if backend == chatmodel.BackendClaude {
		switch record["type"] {
		case "custom-title":
			text, priority = stringValue(record["customTitle"]), 3
		case "ai-title":
			text, priority = stringValue(record["aiTitle"]), 2
		case "summary":
			text, priority = stringValue(record["summary"]), 1
		}
	} else if record["type"] == "session_meta" {
		payload, _ := record["payload"].(map[string]any)
		text, priority = strings.TrimSpace(stringValue(payload["title"])), 1
		if text == "" {
			text = stringValue(payload["thread_name"])
		}
	}
	if text = strings.TrimSpace(text); text != "" && priority >= t.priority {
		t.text, t.priority = text, priority
	}
}

// Codex stores renamed/generated thread titles separately from rollout JSONL.
// Cache the shared title index by file generation, not separately per chat.
type CodexSessionTitles struct {
	mu     sync.Mutex
	path   string
	info   os.FileInfo
	titles map[string]string
}

func codexTitleIndexPath(nativePath string) string {
	for dir := filepath.Dir(nativePath); dir != "." && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if name := filepath.Base(dir); name == "sessions" || name == "archived_sessions" {
			return filepath.Join(filepath.Dir(dir), "session_index.jsonl")
		}
	}
	base := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".codex")
	}
	return filepath.Join(base, "session_index.jsonl")
}

func (c *CodexSessionTitles) Title(nativePath, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	path := codexTitleIndexPath(nativePath)
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if c.path == path && c.info != nil && c.info.Size() == info.Size() && c.info.ModTime().Equal(info.ModTime()) && os.SameFile(c.info, info) {
		return c.titles[sessionID]
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, info.Size()))
	titles := make(map[string]string)
	for {
		line, readErr := reader.ReadBytes('\n')
		var record struct {
			ID    string `json:"id"`
			Title string `json:"thread_name"`
		}
		if json.Unmarshal(line, &record) == nil && record.ID != "" {
			if title := strings.TrimSpace(record.Title); title != "" {
				titles[record.ID] = title
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return ""
			}
			break
		}
	}
	current, err := os.Stat(path)
	if err != nil || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) || !os.SameFile(info, current) {
		return ""
	}
	c.path, c.info, c.titles = path, info, titles
	return titles[sessionID]
}
