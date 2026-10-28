package workspace

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strconv"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

const historyPageSize = 10

var errHistoryCursor = errors.New("invalid history cursor")

func nativeIdentity(sessionID string, turn agent.NativeTurn) string {
	if turn.ID != "" {
		return turn.ID
	}
	sum := sha256.Sum256([]byte(sessionID + ":" + strconv.FormatInt(turn.Start, 10)))
	return fmt.Sprintf("record-%x", sum[:16])
}

func historyTurnID(chatID, identity string) string {
	sum := sha256.Sum256([]byte(chatID + ":" + identity))
	return fmt.Sprintf("history-%x", sum[:16])
}

func (s *Store) hydrateSession(chat chatmodel.Session) chatmodel.Session {
	page, err := s.readHistoryPage(chat, "")
	if err != nil {
		page.History = &chatmodel.HistoryPage{Error: "Native session history is unavailable"}
	}
	return page
}

func (s *Store) readHistoryPage(chat chatmodel.Session, before string) (chatmodel.Session, error) {
	chat.Turns = []chatmodel.Turn{}
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: string(chat.Backend) + "-native"}
	chat.History = &chatmodel.HistoryPage{}
	if chat.NativeSessionID == "" && chat.NativeSessionPath == "" {
		if before != "" {
			return chat, errHistoryCursor
		}
		return chat, nil
	}
	path := chat.NativeSessionPath
	if path == "" {
		directory := ""
		if project, err := s.Project(chat.ProjectID); err == nil {
			directory = project.Path
		}
		var err error
		path, err = agent.FindSessionTranscript(chat.Backend, chat.NativeSessionID, directory)
		if err != nil {
			return chat, err
		}
	}
	chat.NativeSessionPath = path
	page := chat
	err := s.historyCache.read(chat.Backend, path, func(file *nativeHistory) error {
		var err error
		page, err = s.readHistorySnapshot(chat, before, file)
		return err
	})
	if err != nil {
		return chat, err
	}
	return page, nil
}

func (s *Store) readHistorySnapshot(chat chatmodel.Session, before string, file *nativeHistory) (chatmodel.Session, error) {
	chat.Turns = []chatmodel.Turn{}
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: string(chat.Backend) + "-native"}
	chat.History = &chatmodel.HistoryPage{}
	index := file.index
	end := len(index)
	if before != "" {
		end = -1
		for i, turn := range index {
			id := historyTurnID(chat.ID, nativeIdentity(chat.NativeSessionID, turn))
			if id == before {
				end = i
				break
			}
			if "after-"+id == before {
				end = i + 1
				break
			}
		}
		if end < 0 {
			return chat, errHistoryCursor
		}
	}
	start, size, imageBytes := end, 0, 0
	// Decode/cache selected native turns backwards, then build a chat-local page.
	for i := end - 1; i >= 0 && len(chat.Turns) < historyPageSize; i-- {
		native := index[i]
		transcript, err := s.historyCache.transcript(file, i)
		if err != nil {
			return chat, err
		}
		parts := make([]chatmodel.Part, 0, len(transcript.Parts))
		bytes := 0
		for _, part := range transcript.Parts {
			if imageBytes+len(part.ImageURL) > 8*1024*1024 {
				part.ImageURL = ""
			}
			imageBytes += len(part.ImageURL)
			if part.Role == "user" && part.Kind == "context" {
				continue
			}
			parts = append(parts, part)
			bytes += len(part.Text)
		}
		if size > 0 && size+bytes > 400_000 {
			break
		}
		start = i
		if len(parts) == 0 {
			continue
		}
		size += bytes
		identity := nativeIdentity(chat.NativeSessionID, native)
		turn := chatmodel.Turn{ID: historyTurnID(chat.ID, identity), NativeTurnID: identity, NativePromptID: native.PromptID, SourceOffset: native.Start, Backend: chat.Backend, StartedAt: native.StartedAt, FinishedAt: native.FinishedAt, Status: native.Status, Model: native.Model, ReasoningEffort: native.ReasoningEffort, Error: native.Error, Parts: parts}
		if turn.Status == "" {
			turn.Status = chatmodel.StatusCompleted
		}
		// Only a live in-memory request may render an active timer after restart.
		if turn.Status == chatmodel.StatusRunning {
			turn.Status = chatmodel.StatusInterrupted
		}
		populateTurnContent(&turn)
		chat.Turns = append(chat.Turns, turn)
		chat.Transcript.Cut = chat.Transcript.Cut || transcript.Cut
	}
	for i, j := 0, len(chat.Turns)-1; i < j; i, j = i+1, j-1 {
		chat.Turns[i], chat.Turns[j] = chat.Turns[j], chat.Turns[i]
	}
	for i := range chat.Turns {
		turn := &chat.Turns[i]
		turn.PartStart = len(chat.Transcript.Parts)
		chat.Transcript.Parts = append(chat.Transcript.Parts, turn.Parts...)
		turn.PartEnd, turn.InputEnd = len(chat.Transcript.Parts), turn.PartStart
		for turn.InputEnd < turn.PartEnd && chat.Transcript.Parts[turn.InputEnd].Role == "user" {
			turn.InputEnd++
		}
	}
	chat.Transcript.Captured = len(chat.Transcript.Parts)
	chat.History.HasMore = start > 0
	if start < len(index) {
		chat.History.Before = historyTurnID(chat.ID, nativeIdentity(chat.NativeSessionID, index[start]))
	}
	return chat, nil
}

func (s *Store) withRuntimeTurn(chat chatmodel.Session) chatmodel.Session {
	current, ok := s.runtimeTurn(chat.ID)
	if !ok {
		return chat
	}
	matched := -1
	for i := len(chat.Turns) - 1; i >= 0; i-- {
		native := chat.Turns[i]
		if (current.NativeTurnID != "" && current.NativeTurnID == native.NativeTurnID) || (current.NativeTurnID == "" && native.SourceOffset >= current.SourceOffset) {
			matched = i
			break
		}
	}
	if matched >= 0 {
		native := chat.Turns[matched]
		native.ID = current.ID
		native.Status, native.Error, native.PendingApproval = current.Status, current.Error, current.PendingApproval
		native.PermissionMode, native.RequestedModel = current.PermissionMode, current.RequestedModel
		if native.StartedAt == 0 {
			native.StartedAt = current.StartedAt
		}
		if native.FinishedAt == 0 && current.Status != chatmodel.StatusRunning {
			native.FinishedAt = current.FinishedAt
		}
		if native.Model == "" {
			native.Model = current.Model
		}
		chat.Turns[matched] = native
	} else {
		inputs := min(max(0, current.InputEnd-current.PartStart), len(current.Parts))
		current.PartStart = len(chat.Transcript.Parts)
		current.InputEnd = current.PartStart + inputs
		chat.Transcript.Parts = append(chat.Transcript.Parts, current.Parts...)
		current.PartEnd = len(chat.Transcript.Parts)
		chat.Turns = append(chat.Turns, current)
	}
	return chat
}

func historyWindow(chat chatmodel.Session) chatmodel.Session {
	start := max(0, len(chat.Turns)-historyPageSize)
	size := 0
	for i := len(chat.Turns) - 1; i >= start; i-- {
		turn := chat.Turns[i]
		bytes := 0
		for _, part := range chat.Transcript.Parts[max(0, min(turn.PartStart, len(chat.Transcript.Parts))):max(0, min(turn.PartEnd, len(chat.Transcript.Parts)))] {
			bytes += len(part.Text)
		}
		if size > 0 && size+bytes > 400_000 {
			start = i + 1
			break
		}
		size += bytes
	}
	if start == 0 {
		return chat
	}
	previous := chat.Turns[start-1]
	base := max(0, min(chat.Turns[start].PartStart, len(chat.Transcript.Parts)))
	chat.Turns = append([]chatmodel.Turn(nil), chat.Turns[start:]...)
	chat.Transcript.Parts = append([]chatmodel.Part(nil), chat.Transcript.Parts[base:]...)
	for i := range chat.Turns {
		chat.Turns[i].PartStart -= base
		chat.Turns[i].InputEnd -= base
		chat.Turns[i].PartEnd -= base
	}
	first := chat.Turns[0]
	before := first.ID
	if first.NativeTurnID != "" {
		before = historyTurnID(chat.ID, first.NativeTurnID)
	}
	if (first.NativeTurnID == "" || first.Status == chatmodel.StatusRunning) && previous.NativeTurnID != "" {
		// A live request may not yet have a native boundary (notably Claude).
		// Resume after the preceding native turn so trimming cannot skip it.
		before = "after-" + historyTurnID(chat.ID, previous.NativeTurnID)
	}
	chat.History = &chatmodel.HistoryPage{Before: before, HasMore: true}
	return chat
}

func (s *Store) historyPage(id, before string) (chatmodel.Session, error) {
	chat, ok := s.getSession(id)
	if !ok {
		return chat, os.ErrNotExist
	}
	page, err := s.readHistoryPage(chat, before)
	if before == "" && err == nil {
		page = historyWindow(s.withRuntimeTurn(page))
	}
	return page, err
}
