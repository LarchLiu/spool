package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
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
	Model, ReasoningEffort string
	Offset                 int64
}

// CodexMetadata reads only requested turns, extending its search backwards as
// older pages are opened. An append is read forwards from the last complete
// line. It holds neither transcript bodies nor a complete session index.
// One instance belongs to one rollout path and can be shared by its viewers.
type CodexMetadata struct {
	mu       sync.Mutex
	info     os.FileInfo
	end      int64
	floor    int64
	oldestAt int64
	guard    []byte
	turns    map[string]CodexTurnContext
	search   *codexMetadataSearch
}

func (c *CodexMetadata) Read(path string, turns []CodexHistoryTurn) (map[string]CodexTurnContext, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	for range 3 {
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		result, err := c.read(file, info, turns)
		current, statErr := os.Stat(path)
		if statErr != nil || !sameCodexMetadataFile(info, current) {
			c.info = nil
			if statErr != nil {
				return nil, statErr
			}
			if !os.SameFile(info, current) {
				return nil, io.ErrUnexpectedEOF
			}
			continue
		}
		return result, err
	}
	return nil, io.ErrUnexpectedEOF
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
			c.end = end
		} else {
			c.turns = make(map[string]CodexTurnContext)
			c.end, c.floor, c.oldestAt = info.Size(), info.Size(), 0
			c.search = nil
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
		if c.floor == c.end {
			// A trailing incomplete line will be retried on the next append.
			c.end = end
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
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type            string `json:"type"`
		TurnID          string `json:"turn_id"`
		Model           string `json:"model"`
		Effort          string `json:"effort"`
		ReasoningEffort string `json:"reasoning_effort"`
	} `json:"payload"`
}

func (c *CodexMetadata) collect(record codexContextRecord, offset int64, overwrite bool) {
	if record.Type != "turn_context" || record.Payload.TurnID == "" {
		return
	}
	id := record.Payload.TurnID
	if _, exists := c.turns[id]; exists && !overwrite {
		return
	}
	effort := record.Payload.Effort
	if effort == "" {
		effort = record.Payload.ReasoningEffort
	}
	c.turns[id] = CodexTurnContext{Model: record.Payload.Model, ReasoningEffort: effort, Offset: offset}
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
}

// backward locates newlines in fixed-size blocks. Only context records are
// decoded; long tool output is never assembled into a second in-memory body.
func (c *CodexMetadata) backward(file io.ReaderAt, end int64, requested []CodexHistoryTurn) (int64, int64, error) {
	block := make([]byte, codexMetadataBlock)
	if c.search == nil {
		c.search = &codexMetadataSearch{position: end, lineEnd: -1}
	}
	search := c.search
	budgetEnd := max(0, search.position-codexMetadataBudget)
	needle := []byte(`"turn_context"`)
	fragment := func(data []byte) {
		bridge := append(append([]byte{}, data[max(0, len(data)-len(needle)):]...), search.edge...)
		search.candidate = search.candidate || bytes.Contains(data, needle) || bytes.Contains(bridge, needle)
		search.edge = append(append([]byte{}, data[:min(len(data), len(needle))]...), search.edge...)
		search.edge = search.edge[:min(len(search.edge), len(needle))]
	}
	visit := func(start int64) bool {
		if search.candidate {
			var record codexContextRecord
			if json.NewDecoder(io.NewSectionReader(file, start, search.lineEnd-start)).Decode(&record) == nil && record.Type == "turn_context" {
				c.collect(record, start, false)
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
		joined := append(append([]byte{}, edge...), fragment[:min(len(fragment), 16)]...)
		candidate = candidate || codexMetadataCandidate(fragment) || codexMetadataCandidate(joined)
		edge = append(edge[:0], fragment[max(0, len(fragment)-16):]...)
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
	return bytes.Contains(data, []byte(`"turn_context"`)) || bytes.Contains(data, []byte(`"task_started"`))
}

// CodexTurnAfter resolves an unannounced turn from the offset saved before its
// request, stopping at the first native boundary rather than indexing history.
func CodexTurnAfter(path string, offset int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	id := ""
	_, err = codexMetadataForward(file, offset, info.Size(), func(record codexContextRecord, _ int64) bool {
		if record.Payload.TurnID != "" && (record.Type == "turn_context" || record.Type == "event_msg" && record.Payload.Type == "task_started") {
			id = record.Payload.TurnID
		}
		return id != ""
	})
	return id, err
}
