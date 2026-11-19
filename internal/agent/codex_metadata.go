package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"

	chatmodel "spool/internal/model"
)

const (
	codexMetadataBlock = 64 * 1024
	// Missing or not-yet-written IDs must not turn a page read into an
	// unbounded search. A later request can continue from this search boundary.
	codexMetadataBudget = 4 * 1024 * 1024
	codexMetadataTurns  = 4096
)

// CodexTurnContext is the metadata omitted by native turn pages. Offset also
// lets the reader recognize when it has already searched a page's time range.
type CodexTurnContext struct {
	Backend                chatmodel.Backend
	Model, ReasoningEffort string
	Offset                 int64
}

// CodexMetadata reads only requested turns, extending its search backwards as
// older pages are opened. An append is read forwards from the last complete
// line. It holds neither transcript bodies nor a complete session index.
// One instance belongs to one rollout path and can be shared by its viewers.
type CodexMetadata struct {
	mu                     sync.Mutex
	info                   os.FileInfo
	end                    int64 // where an append is read from
	tail                   bool  // end is not yet known to follow a complete line
	floor                  int64
	oldestAt               int64
	guard                  []byte
	turns                  map[string]CodexTurnContext
	search                 *codexMetadataSearch
	activeTurn, activeRoot string
	activeKnown            bool
}

func (c *CodexMetadata) Read(path string, turns []CodexHistoryTurn) (map[string]CodexTurnContext, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result map[string]CodexTurnContext
	for range 3 {
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		result, err = c.read(file, info, turns)
		current, statErr := os.Stat(path)
		if statErr != nil {
			c.info = nil
			return nil, statErr
		}
		if err != nil || sameCodexMetadataFile(info, current) {
			return result, err
		}
		if !os.SameFile(info, current) {
			c.info = nil
			return nil, io.ErrUnexpectedEOF
		}
		if current.Size() <= info.Size() {
			// Rewritten in place: nothing read from it can be trusted.
			c.info, result = nil, nil
		}
		// Otherwise the rollout grew meanwhile, as an active one always does.
		// What was read is complete up to the size it had and is kept; the next
		// pass reads only what was added, after checking that it was an append.
	}
	// A rollout written faster than it is read still has what was found.
	return result, nil
}

// The caller holds c.mu. ReaderAt keeps the scanned byte ranges observable in
// tests, including reads across an appended or partially written JSONL record.
func (c *CodexMetadata) read(file io.ReaderAt, info os.FileInfo, requested []CodexHistoryTurn) (map[string]CodexTurnContext, error) {
	if len(c.turns) > codexMetadataTurns {
		// After browsing many pages, begin a fresh bounded search instead of
		// retaining every visited turn for the lifetime of the process.
		c.info = nil
	}
	unchanged := c.info != nil && sameCodexMetadataFile(c.info, info)
	if !unchanged {
		appendOnly := c.info != nil && os.SameFile(c.info, info) && info.Size() > c.info.Size()
		if appendOnly && len(c.guard) > 0 {
			guard := make([]byte, len(c.guard))
			_, err := file.ReadAt(guard, c.info.Size()-int64(len(guard)))
			appendOnly = err == nil && bytes.Equal(guard, c.guard)
		}
		if appendOnly {
			// Include a previous incomplete line, but never replay completed data.
			end, err := c.append(file, c.end, info.Size())
			if err != nil {
				return nil, err
			}
			c.end, c.tail = end, false
		} else {
			c.turns = make(map[string]CodexTurnContext)
			c.end, c.floor, c.oldestAt = info.Size(), info.Size(), 0
			c.search, c.tail = nil, true
			c.activeTurn, c.activeRoot, c.activeKnown = "", "", false
		}
		c.guard = make([]byte, min(info.Size(), 128))
		if _, err := file.ReadAt(c.guard, info.Size()-int64(len(c.guard))); err != nil {
			return nil, err
		}
		c.info = info
	}
	if len(requested) > 0 && !c.covered(requested) && c.floor > 0 {
		start, end, err := c.backward(file, c.floor, requested)
		if err != nil {
			return nil, err
		}
		if c.tail && end >= 0 {
			// A trailing incomplete line will be retried on the next append.
			c.end, c.tail = end, false
		} else if c.tail {
			// No line ends in what has been searched so far: one record, far
			// longer than a context, which an append continues from here rather
			// than from the start of the rollout.
			c.end = start
		}
		c.floor = start
	}
	result := make(map[string]CodexTurnContext, len(requested))
	for _, turn := range requested {
		if metadata, ok := c.turns[turn.ID]; ok {
			result[turn.ID] = metadata
		}
	}
	return result, nil
}

func sameCodexMetadataFile(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b)
}

func (c *CodexMetadata) covered(turns []CodexHistoryTurn) bool {
	return c.coveredAt(turns, c.floor)
}

func (c *CodexMetadata) coveredAt(turns []CodexHistoryTurn, floor int64) bool {
	all := true
	for _, turn := range turns {
		if _, ok := c.turns[turn.ID]; !ok {
			all = false
			break
		}
	}
	if all || floor == 0 {
		return true
	}
	// Pages are oldest first. Once the oldest requested context is found,
	// missing later IDs cannot be recovered by reading still earlier turns.
	if metadata, ok := c.turns[turns[0].ID]; ok && metadata.Offset >= floor {
		return true
	}
	return turns[0].StartedAt > 0 && c.oldestAt > 0 && c.oldestAt < turns[0].StartedAt
}

type codexContextRecord struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Origin    json.RawMessage `json:"spool_origin"`
	Payload   struct {
		Type            string `json:"type"`
		TurnID          string `json:"turn_id"`
		RootTurnID      string `json:"root_turn_id"`
		Model           string `json:"model"`
		Effort          string `json:"effort"`
		ReasoningEffort string `json:"reasoning_effort"`
		ThreadSettings  struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
		} `json:"thread_settings"`
	} `json:"payload"`
}

func (c *CodexMetadata) collect(record codexContextRecord, offset int64, overwrite bool) bool {
	if record.Type == "event_msg" && record.Payload.Type == "task_complete" {
		if overwrite && (record.Payload.TurnID == c.activeRoot || record.Payload.TurnID == c.activeTurn) {
			c.activeTurn, c.activeRoot, c.activeKnown = "", "", true
		} else if !overwrite && c.search != nil {
			// Walking backwards crosses out of between-turn settings updates.
			c.search.settings = nil
			if !c.activeKnown {
				c.activeTurn, c.activeRoot, c.activeKnown = "", "", true
			}
		}
		return false
	}
	if record.Type == "event_msg" && record.Payload.Type == "thread_settings_applied" {
		settings := CodexTurnContext{Backend: chatmodel.BackendCodex, Model: record.Payload.ThreadSettings.Model, ReasoningEffort: record.Payload.ThreadSettings.ReasoningEffort, Offset: offset}
		if overwrite {
			// Settings restored inside a same-thread internal turn describe its
			// enclosing response. Do not assign ordinary between-turn settings
			// updates to an earlier, unrelated historical turn.
			if c.activeRoot != "" && c.activeRoot != c.activeTurn {
				c.turns[c.activeRoot] = settings
			}
		} else if c.search != nil && c.search.settings == nil {
			c.search.settings = &settings
		}
		return false
	}
	if record.Type == "event_msg" && record.Payload.Type == "task_started" {
		id, root := record.Payload.TurnID, record.Payload.RootTurnID
		if overwrite || !c.activeKnown {
			c.activeTurn, c.activeRoot, c.activeKnown = id, root, true
		}
		if !overwrite && c.search != nil {
			if root != "" && root != id && c.search.settings != nil {
				if _, found := c.turns[root]; !found {
					c.turns[root] = *c.search.settings
				}
			}
			c.search.settings = nil
		}
	}
	origin := decodeOrigin(record.Origin)
	carried := record.Type == "event_msg" && record.Payload.Type == "task_started" && origin.Backend.Valid()
	if record.Type != "turn_context" && !carried || record.Payload.TurnID == "" {
		return false
	}
	id := record.Payload.TurnID
	if _, exists := c.turns[id]; exists && !overwrite {
		return true
	}
	effort := record.Payload.Effort
	if effort == "" {
		effort = record.Payload.ReasoningEffort
	}
	context := CodexTurnContext{Backend: chatmodel.BackendCodex, Model: record.Payload.Model, ReasoningEffort: effort, Offset: offset}
	if carried {
		context.Backend, context.Model, context.ReasoningEffort = origin.Backend, origin.Model, origin.ReasoningEffort
	}
	c.turns[id] = context
	return true
}

func (c *CodexMetadata) append(file io.ReaderAt, start, end int64) (int64, error) {
	return codexMetadataForward(file, start, end, func(record codexContextRecord, offset int64) bool {
		c.collect(record, offset, true)
		return false
	})
}

type codexMetadataSearch struct {
	position, lineEnd, completeEnd int64
	candidate                      bool
	edge                           []byte
	settings                       *CodexTurnContext
}

// backward locates newlines in fixed-size blocks. Only context records are
// decoded; long tool output is never assembled into a second in-memory body.
// It returns how far back it has searched and where the last complete line
// ends, or -1 while no line end has been found.
func (c *CodexMetadata) backward(file io.ReaderAt, end int64, requested []CodexHistoryTurn) (int64, int64, error) {
	block := make([]byte, codexMetadataBlock)
	if c.search == nil {
		c.search = &codexMetadataSearch{position: end, lineEnd: -1, completeEnd: -1}
	}
	search := c.search
	budgetEnd := max(0, search.position-codexMetadataBudget)
	const markerSize = len(`"thread_settings_applied"`)
	fragment := func(data []byte) {
		bridge := append(append([]byte{}, data[max(0, len(data)-markerSize):]...), search.edge...)
		search.candidate = search.candidate || codexMetadataCandidate(data) || codexMetadataCandidate(bridge)
		search.edge = append(append([]byte{}, data[:min(len(data), markerSize)]...), search.edge...)
		search.edge = search.edge[:min(len(search.edge), markerSize)]
	}
	visit := func(start int64) bool {
		if search.candidate {
			var record codexContextRecord
			if json.NewDecoder(io.NewSectionReader(file, start, search.lineEnd-start)).Decode(&record) == nil && c.collect(record, start, false) {
				if stamp := nativeTimestamp(record.Timestamp); stamp > 0 {
					c.oldestAt = stamp
				}
			}
		}
		search.candidate, search.edge = false, nil
		return c.coveredAt(requested, start)
	}
	for search.position > budgetEnd {
		start := max(budgetEnd, search.position-int64(len(block)))
		data := block[:search.position-start]
		if _, err := file.ReadAt(data, start); err != nil {
			c.search = nil
			return end, search.completeEnd, err
		}
		segmentEnd := len(data)
		for i := len(data) - 1; i >= 0; i-- {
			if data[i] != '\n' {
				continue
			}
			if search.lineEnd < 0 {
				search.completeEnd = start + int64(i) + 1
			} else {
				fragment(data[i+1 : segmentEnd])
				lineStart := start + int64(i) + 1
				if visit(lineStart) {
					c.search = nil
					return lineStart, search.completeEnd, nil
				}
			}
			search.lineEnd, segmentEnd = start+int64(i), i
		}
		if search.lineEnd >= 0 {
			fragment(data[:segmentEnd])
		}
		search.position = start
	}
	if search.position == 0 {
		if search.lineEnd >= 0 {
			visit(0)
		}
		c.search = nil
	}
	return search.position, search.completeEnd, nil
}

// Walk complete appended lines in fragments, without assembling tool bodies.
func codexMetadataForward(file io.ReaderAt, start, end int64, visit func(codexContextRecord, int64) bool) (int64, error) {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, start, end-start), codexMetadataBlock)
	lineStart, position := start, start
	candidate := false
	var edge []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		const markerSize = len(`"thread_settings_applied"`)
		joined := append(append([]byte{}, edge...), fragment[:min(len(fragment), markerSize)]...)
		candidate = candidate || codexMetadataCandidate(fragment) || codexMetadataCandidate(joined)
		edge = append(edge[:0], fragment[max(0, len(fragment)-markerSize):]...)
		position += int64(len(fragment))
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF {
				return lineStart, nil
			}
			return lineStart, err
		}
		if candidate {
			var record codexContextRecord
			if json.NewDecoder(io.NewSectionReader(file, lineStart, position-lineStart)).Decode(&record) == nil && visit(record, lineStart) {
				return position, nil
			}
		}
		lineStart, candidate, edge = position, false, edge[:0]
	}
}

func codexMetadataCandidate(data []byte) bool {
	return bytes.Contains(data, []byte(`"turn_context"`)) || bytes.Contains(data, []byte(`"task_started"`)) || bytes.Contains(data, []byte(`"task_complete"`)) || bytes.Contains(data, []byte(`"thread_settings_applied"`))
}

// CodexTurnAfter resolves an unannounced turn from the offset saved before its
// request, stopping at the first native boundary rather than indexing history.
func CodexTurnAfter(path string, offset int64) (string, error) {
	first := ""
	err := codexTurnsAfter(path, offset, func(id string) bool {
		first = id
		return true
	})
	return first, err
}

// CodexTurnIdentity is the identity the rollout gives the turn whose request
// was made at an offset: the ID Codex announced for it where the rollout
// records that, otherwise the first turn begun after the offset. History is
// read back under that identity. It is empty when nothing after the offset
// names a turn, as in a legacy rollout.
func CodexTurnIdentity(path string, offset int64, announced string) (string, error) {
	first, recorded := "", false
	err := codexTurnsAfter(path, offset, func(id string) bool {
		if first == "" {
			first = id
		}
		recorded = id == announced
		return recorded || announced == ""
	})
	if recorded {
		return announced, err
	}
	return first, err
}

// codexTurnsAfter visits the IDs of the turns begun after an offset, oldest
// first, until visit returns true. Only boundary records are decoded.
func codexTurnsAfter(path string, offset int64, visit func(id string) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if offset < 0 || offset > info.Size() {
		return nil
	}
	_, err = codexMetadataForward(file, offset, info.Size(), func(record codexContextRecord, _ int64) bool {
		if record.Payload.TurnID == "" || record.Type != "turn_context" && (record.Type != "event_msg" || record.Payload.Type != "task_started") {
			return false
		}
		return visit(record.Payload.TurnID)
	})
	return err
}
