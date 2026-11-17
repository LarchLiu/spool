package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	chatmodel "spool/internal/model"
)

// SessionArchiver archives provider-owned history without sending a prompt.
type SessionArchiver interface {
	ArchiveSession(context.Context, AgentRequest) error
}

func (r *CLIRunner) ArchiveSession(ctx context.Context, request AgentRequest) error {
	if !chatmodel.ValidID(request.NativeSessionID) {
		return errors.New("invalid session archive")
	}
	switch request.Backend {
	case chatmodel.BackendCodex:
		binary := request.Binary
		if binary == "" {
			var err error
			binary, err = r.Binary(request.Backend)
			if err != nil {
				return err
			}
		}
		history := &CodexHistory{Binary: binary}
		defer history.Close()
		_, err := history.call(ctx, "thread/archive", map[string]any{"threadId": request.NativeSessionID})
		return err
	case chatmodel.BackendClaude:
		return archiveClaudeSession(ctx, request.NativeSessionID)
	default:
		return errors.New("unsupported agent backend")
	}
}

func claudeArchivePath(path, id string) string {
	return filepath.Join(filepath.Dir(path), id, "archived.json")
}

// Match archiveSession(sessionId) in Claude Agent SDK 0.3.326: mark every
// nonempty copy of this session across local projects, without changing any
// transcript. The SDK does not need or check a Claude Code executable/version.
func archiveClaudeSession(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
		return errors.New("invalid Claude session ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := nativeSessionHome(chatmodel.BackendClaude)
	if err != nil {
		return err
	}
	root := filepath.Join(home, "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var paths []string
	for _, dir := range dirs {
		path := filepath.Join(root, dir.Name(), id+".jsonl")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return os.ErrNotExist
	}
	var failures []error
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeClaudeArchive(path, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func writeClaudeArchive(transcript, id string) error {
	path := claudeArchivePath(transcript, id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".archived-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(struct {
		Archived bool   `json:"archived"`
		At       string `json:"at"`
		By       string `json:"by"`
	}{true, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "sdk"})
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// The SDK reads at most 4 KiB and ignores malformed or unreadable archive
// marks. A newer human prompt in the sampled transcript unarchives the chat;
// assistant output, tool results and background messages do not.
func claudeSessionArchived(path, id, head, tail string) bool {
	file, err := os.Open(claudeArchivePath(path, id))
	if err != nil {
		return false
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 4097))
	var mark struct {
		Archived bool   `json:"archived"`
		At       string `json:"at"`
	}
	if err != nil || len(body) > 4096 || json.Unmarshal(body, &mark) != nil || !mark.Archived {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, mark.At)
	if err != nil {
		return false
	}
	for _, sample := range []string{tail, head} {
		for _, record := range claudeSampleRecords(sample) {
			stamp, err := time.Parse(time.RFC3339Nano, stringValue(record["timestamp"]))
			if err == nil && stamp.UnixMilli() > at.UnixMilli() && claudeArchiveHumanPrompt(record) {
				return false
			}
		}
	}
	return true
}

func claudeArchiveHumanPrompt(record map[string]any) bool {
	for _, flag := range []string{"isSidechain", "isCompactSummary", "isVirtual", "isVisibleInTranscriptOnly", "isMeta", "textNotTyped", "handedOff", "queueSkipAttachments"} {
		if value, exists := record[flag]; exists && value != false {
			return false
		}
	}
	message, _ := record["message"].(map[string]any)
	origin, hasOrigin := record["origin"]
	if record["type"] == "attachment" {
		attachment, _ := record["attachment"].(map[string]any)
		if attachment["type"] != "queued_command" {
			return false
		}
		message = map[string]any{"content": attachment["prompt"]}
		origin, hasOrigin = attachment["origin"]
	} else if record["type"] != "user" {
		return false
	}
	if blocks, ok := message["content"].([]any); ok {
		for _, raw := range blocks {
			if block, ok := raw.(map[string]any); ok && block["type"] == "tool_result" {
				return false
			}
		}
	}
	if hasOrigin {
		value, ok := origin.(map[string]any)
		if !ok || value["kind"] != "human" {
			return false
		}
		texts, _ := userContentTexts(message["content"])
		for _, text := range texts {
			if strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "<system-reminder>") {
				return false
			}
		}
		for _, text := range texts {
			if strings.TrimSpace(text) != "" {
				return true
			}
		}
	}
	command := ""
	prompt, media := claudeListUserPrompt(map[string]any{"type": "user", "message": message}, &command)
	return prompt != "" || media != ""
}
