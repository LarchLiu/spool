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
	codexWindowThreads  = 16
	codexWindowTurnPage = 10
	codexWindowMaxPages = 8
	// Internal turns read past a page boundary to keep one response whole.
	codexResponseMaxTurns = 32
	codexHistoryRetry     = 30 * time.Second
	codexHistoryDeadline  = 30 * time.Second
	// How long after a rollout's last write Codex may still be making that
	// write readable through its history requests.
	codexWindowSettle = 2 * time.Second
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
//
// mu guards only this memory. Nothing holds it across a request to Codex, so a
// slow read of one thread never delays a cached answer for another.
type codexWindows struct {
	mu         sync.Mutex
	reading    sync.Mutex // one window read at a time; taken before mu
	reader     codexHistoryReader
	binary     string
	retryAfter time.Time // Codex itself could not be reached
	threads    map[string]*codexWindow
	use        int64
	maxBytes   int
	settle     time.Duration // codexWindowSettle unless a test changes it
}

// A stored window is never changed, apart from its use counter under
// codexWindows.mu, so its turns may be read without the lock.
type codexWindow struct {
	info        os.FileInfo
	readAt      time.Time
	thread      agent.CodexThread
	unsupported bool
	retryAfter  time.Time                // this thread could not be read
	turns       []agent.CodexHistoryTurn // oldest first
	before      string                   // cursor for older turns; "" at the start
	used        int64
}

func (c *codexWindows) rename(threadID, title string) {
	c.reading.Lock()
	defer c.reading.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if window := c.threads[threadID]; window != nil {
		updated := *window
		updated.thread.Name = title
		c.threads[threadID] = &updated
	}
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

func (c *codexWindows) forget(threadID string) {
	c.reading.Lock()
	defer c.reading.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.threads, threadID)
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

// window returns nil when native history cannot serve this thread right now.
func (c *codexWindows) window(binary, threadID string, info os.FileInfo) *codexWindow {
	window, reader := c.cached(binary, threadID, info)
	if reader == nil {
		return window
	}
	// Codex answers one request at a time. Queueing here rather than inside the
	// reader starts this read's deadline only when its own requests begin, and
	// lets a second request for the same thread use the first one's answer.
	c.reading.Lock()
	defer c.reading.Unlock()
	if window, reader = c.cached(binary, threadID, info); reader == nil {
		return window
	}
	next, err := readCodexWindow(reader, threadID, info)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reader != reader {
		// Closed or replaced meanwhile: this answer belongs to no current cache.
		return nil
	}
	switch {
	case err == nil:
	case errors.Is(err, agent.ErrCodexHistoryUnsupported):
		next = &codexWindow{info: info, readAt: next.readAt, unsupported: true}
	default:
		// Keep reading rollouts while Codex is missing or failing, and say so.
		log.Printf("event=codex_history_unavailable thread=%q error=%q", threadID, chatmodel.Clip(err.Error(), 240))
		retry := time.Now().Add(codexHistoryRetry)
		if errors.Is(err, agent.ErrCodexHistoryUnavailable) {
			c.retryAfter = retry
			return nil
		}
		// Codex answered, though not with this thread; the others are unaffected.
		next = &codexWindow{info: info, retryAfter: retry}
	}
	if c.threads == nil {
		c.threads = map[string]*codexWindow{}
	}
	c.use++
	next.used = c.use
	c.threads[threadID] = next
	c.evict()
	if err != nil {
		return nil
	}
	return next
}

// cached answers from memory when it can: with a current window, or with nil
// for a thread native history cannot serve right now. Otherwise it returns the
// reader to read the thread with.
func (c *codexWindows) cached(binary, threadID string, info os.FileInfo) (*codexWindow, codexHistoryReader) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	now := time.Now()
	if current := c.threads[threadID]; current != nil {
		current.used = c.use
		if now.Before(current.retryAfter) {
			return nil, nil
		}
		if current.retryAfter.IsZero() && sameHistoryFile(current.info, info) && !c.unsettled(current, now) {
			if current.unsupported {
				return nil, nil
			}
			return current, nil
		}
	}
	if now.Before(c.retryAfter) {
		return nil, nil
	}
	return nil, c.reader
}

// A rollout's size and time identify what was written, not what Codex had made
// readable when it was asked. A window read right after the last write may lack
// that write, and nothing would change the file again to correct it, so such a
// window is read once more after the rollout has been quiet for a while.
func (c *codexWindows) unsettled(window *codexWindow, now time.Time) bool {
	settle := c.settle
	if settle <= 0 {
		settle = codexWindowSettle
	}
	modified := window.info.ModTime()
	return window.readAt.Sub(modified) < settle && now.Sub(modified) >= settle
}

// readCodexWindow reads a thread's newest turns. The caller holds no cache lock.
func readCodexWindow(reader codexHistoryReader, threadID string, info os.FileInfo) (*codexWindow, error) {
	next := &codexWindow{info: info, readAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), codexHistoryDeadline)
	defer cancel()
	thread, err := reader.Thread(ctx, threadID)
	if err != nil {
		return next, err
	}
	if !thread.Paginated {
		return next, agent.ErrCodexHistoryUnsupported
	}
	next.thread = thread
	cursor := ""
	for range codexWindowMaxPages {
		page, err := reader.Turns(ctx, threadID, cursor, codexWindowTurnPage)
		if err != nil {
			return next, err
		}
		summarizeTurns(page.Turns)
		next.turns = append(page.Turns, next.turns...)
		next.before, cursor = page.Before, page.Before
		if cursor == "" || len(page.Turns) == 0 || turnBytes(next.turns) >= historyPageBytes {
			break
		}
	}
	if err := completeCodexResponse(ctx, reader, threadID, &next.turns, &next.before); err != nil {
		return next, err
	}
	// An index that has not been built reads as an empty thread. The rollout
	// is the record; let the parser decide whether it really is empty.
	if len(next.turns) == 0 {
		return next, agent.ErrCodexHistoryUnsupported
	}
	return next, nil
}

// Native pagination counts internal turns. Do not split one logical response
// between transport pages: peek at the next record and consume it only while it
// shares the oldest loaded root. The unconsumed cursor remains the page anchor.
// A root can be loaded before its older internal turns, so a turn that is its
// own root needs the peek too. A response longer than the bound is split
// rather than read one request at a time until the deadline.
func completeCodexResponse(ctx context.Context, reader codexHistoryReader, threadID string, turns *[]agent.CodexHistoryTurn, before *string) error {
	if len(*turns) == 0 || (*turns)[0].RootID == "" {
		return nil
	}
	root := (*turns)[0].RootID
	for range codexResponseMaxTurns {
		if *before == "" {
			return nil
		}
		page, err := reader.Turns(ctx, threadID, *before, 1)
		if err != nil {
			return err
		}
		if len(page.Turns) == 0 || page.Turns[0].RootID != root {
			return nil
		}
		if page.Before == *before {
			return errors.New("Codex repeated its response continuation cursor")
		}
		summarizeTurns(page.Turns)
		*turns = append(page.Turns, (*turns)...)
		*before = page.Before
	}
	return nil
}

// currentReader is the reader an explicit page or item request uses. Such a
// request reaches Codex without holding the window cache.
func (c *codexWindows) currentReader() codexHistoryReader {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reader
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
	window := s.codexHistory.window(binary, chat.NativeSessionID, info)
	if window == nil {
		return chat, false
	}
	chat = s.codexHistorySession(chat, window.turns, window.before, true)
	if window.thread.Name != "" {
		chat.Title = window.thread.Name
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

// codexHistorySession shows native turns as a chat. A single tool's detail
// shows neither model nor effort, so it passes withMetadata false and skips the
// rollout search for them.
func (s *Store) codexHistorySession(chat chatmodel.Session, turns []agent.CodexHistoryTurn, before string, withMetadata bool) chatmodel.Session {
	// Native pages omit per-turn model/effort. Search only as far back as this
	// page needs, and scan new records on append. Metadata failures must not
	// hide readable native items or apply today's settings to historical turns.
	var metadata map[string]agent.CodexTurnContext
	if withMetadata {
		metadata, _ = s.historyCache.codexTurnMetadata(chat.NativeSessionPath, turns)
	}
	chat.Turns = make([]chatmodel.Turn, 0, len(turns))
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: string(chat.Backend) + "-native"}
	for _, group := range codexResponseGroups(turns) {
		// Internal turns carry process items; the root carries the final reply
		// and authoritative completion. Both describe one user-visible response.
		native := group[len(group)-1]
		identity := native.RootID
		if identity == "" {
			identity = native.ID
		}
		context := metadata[identity]
		// What the user sent opens the response, whichever turn recorded it;
		// the root's own output closes it.
		var input, parts []chatmodel.Part
		var identities []string
		for _, member := range group {
			identities = append(identities, member.ID)
			// An internal turn only supplies what the root's record lacks.
			other := metadata[member.ID]
			if context.Model == "" && other.Model != "" {
				context.Model = other.Model
				if context.ReasoningEffort == "" {
					context.ReasoningEffort = other.ReasoningEffort
				}
			}
			if !context.Backend.Valid() {
				context.Backend = other.Backend
			}
			if native.StartedAt == 0 || member.StartedAt > 0 && member.StartedAt < native.StartedAt {
				native.StartedAt = member.StartedAt
			}
			// Window parts are shared between responses; copy before use. Tool
			// locators keep their actual owner, rather than the merged root ID.
			owned := cloneTranscript(chatmodel.Transcript{Parts: member.Parts}).Parts
			sent := 0
			for i := range owned {
				owned[i].NativeTurnID = member.ID
				if sent == i && owned[i].Role == "user" {
					sent++
				}
			}
			if member.ID == identity {
				input = append(owned[:sent:sent], input...)
			} else {
				input = append(input, owned[:sent]...)
			}
			parts = append(parts, owned[sent:]...)
		}
		parts = append(input, parts...)
		backend := context.Backend
		if !backend.Valid() {
			backend = chat.Backend
		}
		for i := range parts {
			if parts[i].Role == "assistant" && parts[i].Model == "" {
				parts[i].Model = context.Model
			}
		}
		// These turns have no rollout offset; -1 keeps offset-based actor matching away.
		turn := chatmodel.Turn{ID: historyTurnID(chat.ID, identity), NativeTurnID: identity, NativeTurnIDs: identities, SourceOffset: -1, Backend: backend,
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

func codexResponseGroups(turns []agent.CodexHistoryTurn) [][]agent.CodexHistoryTurn {
	var groups [][]agent.CodexHistoryTurn
	positions := map[string]int{}
	for _, turn := range turns {
		root := turn.RootID
		if root == "" {
			root = turn.ID
		}
		index, found := positions[root]
		if !found {
			index = len(groups)
			positions[root] = index
			groups = append(groups, nil)
		}
		group := groups[index]
		// Keep the root's final output after the internal process even when
		// equal native timestamps put the root before its children in a page.
		if len(group) > 0 && group[len(group)-1].ID == root {
			group = append(group[:len(group)-1], turn, group[len(group)-1])
		} else {
			group = append(group, turn)
		}
		groups[index] = group
	}
	return groups
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
	reader := s.codexHistory.currentReader()
	if reader == nil {
		return chat, errors.New("Codex history reader is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, codexHistoryDeadline)
	defer cancel()
	var turns []agent.CodexHistoryTurn
	cursor := anchor.Cursor
	for range codexWindowMaxPages {
		page, err := reader.Turns(ctx, chat.NativeSessionID, cursor, codexWindowTurnPage)
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
	if err := completeCodexResponse(ctx, reader, chat.NativeSessionID, &turns, &cursor); err != nil {
		return chat, err
	}
	chat = s.codexHistorySession(chat, turns, cursor, true)
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
	reader := s.codexHistory.currentReader()
	if reader == nil {
		return chat, errors.New("Codex history reader is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, codexHistoryDeadline)
	defer cancel()
	parts, err := reader.Item(ctx, chat.NativeSessionID, locator.Turn, locator.Item)
	if err != nil {
		return chat, err
	}
	return s.codexHistorySession(chat, []agent.CodexHistoryTurn{{ID: locator.Turn, Parts: parts}}, "", false), nil
}
