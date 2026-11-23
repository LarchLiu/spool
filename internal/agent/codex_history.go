package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	chatmodel "spool/internal/model"
)

// ErrCodexHistoryUnsupported reports a thread, or an installed Codex, that
// cannot be read through paginated history. Callers read its rollout instead.
var ErrCodexHistoryUnsupported = errors.New("Codex paginated history is unavailable for this thread")

// ErrCodexHistoryUnavailable reports that Codex itself could not be started or
// initialized: no other thread can expect to fare better right now. An error
// without it concerns the one request that was made, whose process is
// restarted by the next request if it had to be stopped.
var ErrCodexHistoryUnavailable = errors.New("Codex history reader could not be started")

const (
	codexHistoryIdle     = 2 * time.Minute
	codexHistoryPageSize = 100 // app-server's maximum for turns and items
)

// CodexHistory reads stored threads the way Codex's own clients do when they
// only view one: thread/read for metadata, then thread/turns/list. It never
// resumes a thread, so a read does not take the
// thread's writer lease, start its MCP servers or append to its rollout.
// One app-server process serves every read and exits after a short idle time.
type CodexHistory struct {
	// Binary is the optional configured CLI executable.
	Binary string

	mu             sync.Mutex
	process        *agentProcess
	cancel         context.CancelFunc
	timer          *time.Timer
	closed         bool
	idleGeneration uint64
}

// CodexThread is a stored thread's metadata.
type CodexThread struct {
	ID, Path, Name, Directory, Model, ReasoningEffort string
	// Paginated is false for a legacy thread: its turns carry no tool items
	// and it has no item pages.
	Paginated bool
	UpdatedAt int64 // milliseconds
}

// CodexHistoryTurn is one stored turn with its complete items as parts.
type CodexHistoryTurn struct {
	ID                    string
	RootID                string
	Status                chatmodel.SessionStatus
	Error                 string
	StartedAt, FinishedAt int64 // milliseconds
	Parts                 []chatmodel.Part
}

// CodexTurnPage holds consecutive turns, oldest first. Before continues with
// the turns preceding them; it is empty at the start of the thread.
type CodexTurnPage struct {
	Turns  []CodexHistoryTurn
	Before string
}

func (h *CodexHistory) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	h.stopLocked()
}

// The caller holds h.mu.
func (h *CodexHistory) stopLocked() {
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	if h.process == nil {
		return
	}
	process, cancel := h.process, h.cancel
	h.process, h.cancel = nil, nil
	// End of input lets the server finish its own bookkeeping; only a stuck
	// server is killed.
	_ = process.stdin.Close()
	select {
	case <-process.done:
	case <-time.After(agentExitGracePeriod):
	}
	cancel()
	<-process.done
}

// The caller holds h.mu.
func (h *CodexHistory) startLocked(ctx context.Context) error {
	if h.closed {
		return errors.New("Codex history reader is closed")
	}
	if h.process != nil {
		select {
		case <-h.process.done:
			h.stopLocked()
		default:
			return nil
		}
	}
	binary := h.Binary
	var err error
	if binary == "" {
		binary, err = (&CLIRunner{}).Binary(chatmodel.BackendCodex)
	} else {
		binary, err = exec.LookPath(binary)
	}
	if err != nil {
		return err
	}
	directory, err := WorkingDirectory("")
	if err != nil {
		return err
	}
	// The process outlives this request; only Close or the idle timer end it.
	processCtx, cancel := context.WithCancel(context.Background())
	process, err := startAgentProcess(processCtx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, WorkingDirectory: directory})
	if err != nil {
		cancel()
		return err
	}
	h.process, h.cancel = process, cancel
	if err := process.initializeCodex(ctx); err != nil {
		h.stopLocked()
		return err
	}
	return nil
}

func (h *CodexHistory) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.startLocked(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCodexHistoryUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	process := h.process
	process.nextID++
	id := process.nextID
	fail := func(err error) (map[string]any, error) {
		// A reply may still be in flight; never match it to a later request.
		h.stopLocked()
		return nil, err
	}
	if err := process.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return fail(err)
	}
	for {
		value, err := process.read(ctx)
		if err != nil {
			return fail(err)
		}
		if value["id"] != nil && value["method"] != nil {
			if err := process.denyCodexRequest(value); err != nil {
				return fail(err)
			}
			continue
		}
		if value["id"] != float64(id) {
			continue
		}
		if h.timer != nil {
			h.timer.Stop()
		}
		h.idleGeneration++
		generation := h.idleGeneration
		h.timer = time.AfterFunc(codexHistoryIdle, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.process == process && h.idleGeneration == generation {
				h.stopLocked()
			}
		})
		if failure, ok := value["error"].(map[string]any); ok {
			code, _ := failure["code"].(float64)
			message := chatmodel.Clip(stringValue(failure["message"]), maxStderrBytes)
			if code == -32601 || strings.Contains(message, "not supported") {
				return nil, fmt.Errorf("%w: %s", ErrCodexHistoryUnsupported, message)
			}
			return nil, &codexRPCError{method: method, message: message, code: code}
		}
		result, ok := value["result"].(map[string]any)
		if !ok {
			return nil, &codexRPCError{method: method, message: "returned no result"}
		}
		return result, nil
	}
}

// Thread reads a thread's metadata only.
func (h *CodexHistory) Thread(ctx context.Context, threadID string) (CodexThread, error) {
	if !chatmodel.ValidID(threadID) {
		return CodexThread{}, errors.New("invalid Codex thread ID")
	}
	result, err := h.call(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false})
	if err != nil {
		return CodexThread{}, err
	}
	thread, _ := result["thread"].(map[string]any)
	if stringValue(thread["id"]) != threadID {
		return CodexThread{}, errors.New("Codex thread/read returned a different thread")
	}
	updated, _ := thread["updatedAt"].(float64)
	return CodexThread{ID: threadID, Path: stringValue(thread["path"]), Name: strings.Join(strings.Fields(stringValue(thread["name"])), " "),
		Directory: stringValue(thread["cwd"]), Model: stringValue(thread["model"]), ReasoningEffort: stringValue(thread["reasoningEffort"]),
		Paginated: stringValue(thread["historyMode"]) == "paginated", UpdatedAt: int64(updated) * 1000}, nil
}

// Turns reads up to limit turns ending just before the cursor, or the newest
// turns for an empty cursor, with every item of each turn.
func (h *CodexHistory) Turns(ctx context.Context, threadID, before string, limit int) (CodexTurnPage, error) {
	if !chatmodel.ValidID(threadID) {
		return CodexTurnPage{}, errors.New("invalid Codex thread ID")
	}
	params := map[string]any{"threadId": threadID, "limit": max(1, min(limit, codexHistoryPageSize)), "itemsView": "full", "sortDirection": "desc"}
	if before != "" {
		params["cursor"] = before
	}
	result, err := h.call(ctx, "thread/turns/list", params)
	if err != nil {
		return CodexTurnPage{}, err
	}
	data, ok := result["data"].([]any)
	if !ok {
		return CodexTurnPage{}, errors.New("Codex thread/turns/list returned invalid data")
	}
	page := CodexTurnPage{Before: stringValue(result["nextCursor"]), Turns: make([]CodexHistoryTurn, 0, len(data))}
	if page.Before != "" && page.Before == before {
		return CodexTurnPage{}, errors.New("Codex thread/turns/list repeated its pagination cursor")
	}
	for i := len(data) - 1; i >= 0; i-- {
		raw, _ := data[i].(map[string]any)
		id := stringValue(raw["id"])
		if id == "" {
			return CodexTurnPage{}, errors.New("Codex thread/turns/list returned a turn without an ID")
		}
		if stringValue(raw["itemsView"]) != "full" {
			return CodexTurnPage{}, fmt.Errorf("%w: turn items were not returned", ErrCodexHistoryUnsupported)
		}
		turn := CodexHistoryTurn{ID: id, RootID: stringValue(raw["rootTurnId"]), StartedAt: codexSeconds(raw["startedAt"]), FinishedAt: codexSeconds(raw["completedAt"])}
		switch stringValue(raw["status"]) {
		case "completed":
			turn.Status = chatmodel.StatusCompleted
		case "interrupted":
			turn.Status = chatmodel.StatusStopped
		case "failed":
			turn.Status = chatmodel.StatusFailed
			turn.Error = chatmodel.Clip(nestedString(raw, "error", "message"), 8192)
		default:
			turn.Status = chatmodel.StatusRunning
		}
		items, _ := raw["items"].([]any)
		turn.Parts = codexThreadParts(items, turn.StartedAt)
		page.Turns = append(page.Turns, turn)
	}
	return page, nil
}

func codexSeconds(value any) int64 {
	seconds, _ := value.(float64)
	return int64(seconds) * 1000
}

// Item reads a tool directly by its persisted identity. Older app-servers can
// still locate it within its turn; neither path walks unrelated turns.
func (h *CodexHistory) Item(ctx context.Context, threadID, turnID, itemID string) ([]chatmodel.Part, error) {
	if !chatmodel.ValidID(threadID) || turnID == "" || itemID == "" {
		return nil, errors.New("invalid Codex item identity")
	}
	result, err := h.call(ctx, "thread/items/read", map[string]any{"threadId": threadID, "turnId": turnID, "itemIds": []string{itemID}})
	if err == nil {
		return codexItemFromEntries(result, turnID, itemID), nil
	}
	if !errors.Is(err, ErrCodexHistoryUnsupported) {
		return nil, err
	}
	cursor := ""
	for {
		params := map[string]any{"threadId": threadID, "turnId": turnID, "limit": codexHistoryPageSize}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err = h.call(ctx, "thread/items/list", params)
		if err != nil {
			return nil, err
		}
		if parts := codexItemFromEntries(result, turnID, itemID); len(parts) > 0 {
			return parts, nil
		}
		next := stringValue(result["nextCursor"])
		if next == "" || next == cursor {
			return nil, nil
		}
		cursor = next
	}
}

func codexItemFromEntries(result map[string]any, turnID, itemID string) []chatmodel.Part {
	entries, _ := result["data"].([]any)
	for _, value := range entries {
		entry, _ := value.(map[string]any)
		item, _ := entry["item"].(map[string]any)
		if stringValue(entry["turnId"]) == turnID && stringValue(item["id"]) == itemID {
			return codexTranscriptParts(item, "")
		}
	}
	return nil
}

// codexThreadParts converts a turn's app-server items. User input keeps the
// same projection as a rollout's, so both sources show the same message.
func codexThreadParts(items []any, startedAt int64) []chatmodel.Part {
	var parts []chatmodel.Part
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		switch codexItemKind(item) {
		case "usermessage":
			content, _ := item["content"].([]any)
			var input []chatmodel.Part
			for _, value := range content {
				block, _ := value.(map[string]any)
				switch stringValue(block["type"]) {
				case "text":
					if text := stringValue(block["text"]); text != "" {
						input = append(input, chatmodel.Part{Role: "user", Kind: "text", Text: text})
					}
				case "image":
					input = append(input, chatmodel.Part{Role: "user", Kind: "image", Text: "[image]", ImageURL: nativeImageURL(stringValue(block["url"]))})
				case "localImage":
					filePath := stringValue(block["path"])
					name := path.Base(strings.ReplaceAll(filePath, "\\", "/"))
					if validUserFile(name, filePath) {
						input = append(input, chatmodel.Part{Role: "user", Kind: "image", Text: "[image]", Name: name, FilePath: filePath})
					}
				}
			}
			stamp := startedAt
			if at, ok := item["startedAtMs"].(float64); ok && at > 0 {
				stamp = int64(at)
			}
			for _, part := range projectUserParts(input) {
				part.Timestamp = stamp
				if part.Text != "" || part.ImageURL != "" {
					parts = append(parts, part)
				}
			}
		case "agentmessage":
			if text := stringValue(item["text"]); text != "" {
				parts = append(parts, chatmodel.Part{ID: stringValue(item["id"]), Phase: stringValue(item["phase"]), Role: "assistant", Kind: "text", Text: text})
			}
		default:
			for _, part := range codexTranscriptParts(item, "") {
				if part.Text != "" || part.ImageURL != "" || part.Kind == "tool_use" && part.Name != "" && part.ID != "" || part.Kind == "tool_result" && part.Native != nil {
					parts = append(parts, part)
				}
			}
		}
	}
	return parts
}
