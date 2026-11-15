package workspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

const (
	codexWindowThreads   = 16
	codexWindowTurnPage  = 10
	codexWindowMaxPages  = 8
	codexHistoryRetry    = 30 * time.Second
	codexHistoryDeadline = 30 * time.Second
)

// codexHistoryReader is agent.CodexHistory; tests substitute a fake.
type codexHistoryReader interface {
	Thread(context.Context, string) (agent.CodexThread, error)
	Turns(context.Context, string, string, int) (agent.CodexTurnPage, error)
	Item(context.Context, string, string, string) ([]chatmodel.Part, error)
	Close()
}

// codexWindows holds, per Codex thread, the newest turns read through native
// pagination. Older requests carry their upstream cursor and are not retained.
// A changed rollout re-reads the newest page. Per-turn metadata is joined from
// a page-scoped rollout reader after copying the window into a response.
// A thread that cannot be paged is remembered as such, and its rollout bodies
// are read by the Go parser instead.
type codexWindows struct {
	mu         sync.Mutex
	reader     codexHistoryReader
	binary     string
	retryAfter time.Time
	threads    map[string]*codexWindow
	use        int64
	maxBytes   int
}

type codexWindow struct {
	info        os.FileInfo
	thread      agent.CodexThread
	unsupported bool
	turns       []agent.CodexHistoryTurn // oldest first
	before      string                   // cursor for older turns; "" at the start
	used        int64
}

func (c *codexWindows) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reader != nil {
		c.reader.Close()
		c.reader = nil
	}
	c.threads = nil
}

func turnBytes(turns []agent.CodexHistoryTurn) int {
	size := 0
	for _, turn := range turns {
		for _, part := range turn.Parts {
			size += len(part.Text) + len(part.ImageURL)
			for _, image := range part.Images {
				size += len(image)
			}
		}
	}
	return size
}

func summarizeTurns(turns []agent.CodexHistoryTurn) {
	for i := range turns {
		for j := range turns[i].Parts {
			if part := &turns[i].Parts[j]; toolPart(*part) {
				part.Tool = summarizeTool(*part)
			}
		}
	}
}

// The caller holds c.mu. It returns nil when native history cannot serve this
// thread right now.
func (c *codexWindows) window(binary, threadID string, info os.FileInfo) *codexWindow {
	if c.reader != nil && c.binary != binary {
		c.reader.Close()
		c.reader = nil
		c.threads, c.retryAfter = nil, time.Time{}
	}
	if c.reader == nil {
		c.reader, c.binary = &agent.CodexHistory{Binary: binary}, binary
	}
	if c.threads == nil {
		c.threads = map[string]*codexWindow{}
	}
	c.use++
	current := c.threads[threadID]
	if current != nil && sameHistoryFile(current.info, info) {
		current.used = c.use
		if current.unsupported {
			return nil
		}
		return current
	}
	if time.Now().Before(c.retryAfter) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexHistoryDeadline)
	defer cancel()
	next := &codexWindow{info: info, used: c.use}
	fail := func(err error) *codexWindow {
		if errors.Is(err, agent.ErrCodexHistoryUnsupported) {
			next.unsupported = true
			c.store(threadID, next)
			return nil
		}
		// Keep reading rollouts while Codex is missing or failing, and say so once.
		c.retryAfter = time.Now().Add(codexHistoryRetry)
		log.Printf("event=codex_history_unavailable error=%q", chatmodel.Clip(err.Error(), 240))
		return nil
	}
	thread, err := c.reader.Thread(ctx, threadID)
	if err != nil {
		return fail(err)
	}
	if !thread.Paginated {
		return fail(agent.ErrCodexHistoryUnsupported)
	}
	next.thread = thread
	cursor := ""
	for range codexWindowMaxPages {
		page, err := c.reader.Turns(ctx, threadID, cursor, codexWindowTurnPage)
		if err != nil {
			return fail(err)
		}
		summarizeTurns(page.Turns)
		next.turns = append(page.Turns, next.turns...)
		next.before, cursor = page.Before, page.Before
		if cursor == "" || len(page.Turns) == 0 || turnBytes(next.turns) >= historyPageBytes {
			break
		}
	}
	// An index that has not been built reads as an empty thread. The rollout
	// is the record; let the parser decide whether it really is empty.
	if len(next.turns) == 0 {
		return fail(agent.ErrCodexHistoryUnsupported)
	}
	c.store(threadID, next)
	return next
}

// The caller holds c.mu.
func (c *codexWindows) store(threadID string, window *codexWindow) {
	c.threads[threadID] = window
	c.evict()
}

func (c *codexWindows) setMaxBytes(limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxBytes = limit
	c.evict()
}

// The caller holds c.mu. One oversized response is served without caching it.
func (c *codexWindows) evict() {
	limit := c.maxBytes
	if limit <= 0 {
		limit = historyCacheBytes
	}
	for {
		bytes := 0
		for _, value := range c.threads {
			bytes += turnBytes(value.turns)
		}
		if len(c.threads) <= codexWindowThreads && bytes <= limit {
			break
		}
		oldest, id := int64(0), ""
		for key, value := range c.threads {
			if id == "" || value.used < oldest {
				oldest, id = value.used, key
			}
		}
		delete(c.threads, id)
	}
}

func (s *Store) codexBinary() (string, bool) {
	agents, err := s.Agents()
	if err != nil {
		return "", false
	}
	for _, info := range agents {
		if info.Backend == chatmodel.BackendCodex {
			return info.Executable, info.Enabled
		}
	}
	return "", false
}

// readCodexHistory fills a chat from native paginated history. It reports
// false when the thread must be read from its rollout instead.
func (s *Store) readCodexHistory(chat chatmodel.Session, path string) (chatmodel.Session, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return chat, false
	}
	binary, enabled := s.codexBinary()
	if !enabled {
		return chat, false
	}
	s.codexHistory.mu.Lock()
	window := s.codexHistory.window(binary, chat.NativeSessionID, info)
	var turns []agent.CodexHistoryTurn
	var thread agent.CodexThread
	before := ""
	if window != nil {
		turns, thread, before = window.turns, window.thread, window.before
	}
	s.codexHistory.mu.Unlock()
	if window == nil {
		return chat, false
	}
	chat = s.codexHistorySession(chat, turns, before)
	if thread.Name != "" {
		chat.Title = thread.Name
	} else if title := s.historyCache.titles.Title(path, chat.NativeSessionID); title != "" {
		chat.Title = title
	}
	return chat, true
}

// A transport anchor carries Codex's opaque continuation cursor, scoped to the
// authorized chat. It remains usable after cache eviction and new activity.
type codexPageAnchor struct {
	Chat   string `json:"c"`
	Cursor string `json:"p"`
}

func nativeHistoryCursor(chatID, cursor string) string {
	data, _ := json.Marshal(codexPageAnchor{Chat: chatID, Cursor: cursor})
	return "native-" + base64.RawURLEncoding.EncodeToString(data)
}

func (s *Store) codexHistorySession(chat chatmodel.Session, turns []agent.CodexHistoryTurn, before string) chatmodel.Session {
	// Native pages omit per-turn model/effort. Search only as far back as this
	// page needs, and scan new records on append. Metadata failures must not
	// hide readable native items or apply today's settings to historical turns.
	metadata, _ := s.historyCache.codexTurnMetadata(chat.NativeSessionPath, turns)
	chat.Turns = make([]chatmodel.Turn, 0, len(turns))
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: string(chat.Backend) + "-native"}
	for _, native := range turns {
		// Window parts are shared between responses; copy before use.
		parts := cloneTranscript(chatmodel.Transcript{Parts: native.Parts}).Parts
		context := metadata[native.ID]
		for i := range parts {
			if parts[i].Role == "assistant" && parts[i].Model == "" {
				parts[i].Model = context.Model
			}
		}
		// These turns have no rollout offset; -1 keeps offset-based actor matching away.
		turn := chatmodel.Turn{ID: historyTurnID(chat.ID, native.ID), NativeTurnID: native.ID, SourceOffset: -1, Backend: chat.Backend,
			NativeHistory: true, Model: context.Model, ReasoningEffort: context.ReasoningEffort,
			StartedAt: native.StartedAt, FinishedAt: native.FinishedAt, Status: native.Status, Error: native.Error, Parts: parts}
		// Only a live in-memory request may render an active timer.
		if turn.Status == chatmodel.StatusRunning {
			turn.Status = chatmodel.StatusInterrupted
		}
		populateTurnContent(&turn)
		turn.PartStart = len(chat.Transcript.Parts)
		chat.Transcript.Parts = append(chat.Transcript.Parts, parts...)
		turn.PartEnd, turn.InputEnd = len(chat.Transcript.Parts), turn.PartStart
		for turn.InputEnd < turn.PartEnd && chat.Transcript.Parts[turn.InputEnd].Role == "user" {
			turn.InputEnd++
		}
		chat.Turns = append(chat.Turns, turn)
	}
	chat.Transcript.Captured = len(chat.Transcript.Parts)
	chat.History = &chatmodel.HistoryState{Native: true, HasMore: before != ""}
	if before != "" {
		chat.History.Before = nativeHistoryCursor(chat.ID, before)
	}
	return chat
}

// codexHistoryPage reads a bounded page directly. It does not change the newest
// window or the base transcript of an active turn.
func (s *Store) codexHistoryPage(ctx context.Context, chat chatmodel.Session, before string) (chatmodel.Session, error) {
	encoded, ok := strings.CutPrefix(before, "native-")
	if len(encoded) > 16384 {
		return chat, errHistoryCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	var anchor codexPageAnchor
	if !ok || err != nil || json.Unmarshal(data, &anchor) != nil || anchor.Chat != chat.ID || anchor.Cursor == "" || chat.Backend != chatmodel.BackendCodex {
		return chat, errHistoryCursor
	}
	if _, enabled := s.codexBinary(); !enabled {
		return chat, errors.New("Codex is disabled")
	}
	s.codexHistory.mu.Lock()
	defer s.codexHistory.mu.Unlock()
	if s.codexHistory.reader == nil {
		return chat, errors.New("Codex history reader is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, codexHistoryDeadline)
	defer cancel()
	var turns []agent.CodexHistoryTurn
	cursor := anchor.Cursor
	for range codexWindowMaxPages {
		page, err := s.codexHistory.reader.Turns(ctx, chat.NativeSessionID, cursor, codexWindowTurnPage)
		if err != nil {
			return chat, err
		}
		summarizeTurns(page.Turns)
		turns = append(page.Turns, turns...)
		previous := cursor
		cursor = page.Before
		if cursor == "" || cursor == previous || turnBytes(turns) >= historyPageBytes {
			break
		}
	}
	chat = s.codexHistorySession(chat, turns, cursor)
	if err := s.attributeTurns(&chat); err != nil {
		return chat, err
	}
	chat.NativeSessionID = ""
	return historyPage(chat, "")
}

type codexToolLocator struct {
	Chat string `json:"c"`
	Turn string `json:"t"`
	Item string `json:"i"`
	Kind string `json:"k"`
}

func nativeToolRef(chatID, turnID string, part chatmodel.Part) string {
	data, _ := json.Marshal(codexToolLocator{Chat: chatID, Turn: turnID, Item: part.ID, Kind: part.Kind})
	return "native-tool-" + base64.RawURLEncoding.EncodeToString(data)
}

func (s *Store) codexToolSnapshot(ctx context.Context, chat chatmodel.Session, ref string) (chatmodel.Session, error) {
	encoded, ok := strings.CutPrefix(ref, "native-tool-")
	if !ok || len(encoded) > 16384 {
		return chat, errHistoryCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	var locator codexToolLocator
	if err != nil || json.Unmarshal(data, &locator) != nil || locator.Chat != chat.ID || locator.Turn == "" || locator.Item == "" ||
		(locator.Kind != "tool_use" && locator.Kind != "tool_result" && locator.Kind != "thinking") || chat.Backend != chatmodel.BackendCodex {
		return chat, errHistoryCursor
	}
	if _, enabled := s.codexBinary(); !enabled {
		return chat, errors.New("Codex is disabled")
	}
	s.codexHistory.mu.Lock()
	defer s.codexHistory.mu.Unlock()
	if s.codexHistory.reader == nil {
		return chat, errors.New("Codex history reader is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, codexHistoryDeadline)
	defer cancel()
	parts, err := s.codexHistory.reader.Item(ctx, chat.NativeSessionID, locator.Turn, locator.Item)
	if err != nil {
		return chat, err
	}
	return s.codexHistorySession(chat, []agent.CodexHistoryTurn{{ID: locator.Turn, Parts: parts}}, ""), nil
}
