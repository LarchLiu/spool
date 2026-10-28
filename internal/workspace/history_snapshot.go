package workspace

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

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
	snapshot, err := s.readHistory(chat)
	if err != nil {
		snapshot.History = &chatmodel.HistoryState{Error: "Native session history is unavailable"}
	}
	return snapshot
}

func (s *Store) readHistory(chat chatmodel.Session) (chatmodel.Session, error) {
	chat.Turns = []chatmodel.Turn{}
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: string(chat.Backend) + "-native"}
	chat.History = nil
	if chat.NativeSessionID == "" && chat.NativeSessionPath == "" {
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
	snapshot := chat
	err := s.historyCache.read(chat.Backend, path, func(file *nativeHistory) error {
		var err error
		snapshot, err = s.readHistorySnapshot(chat, file)
		return err
	})
	if err != nil {
		return chat, err
	}
	if chat.Backend == chatmodel.BackendCodex {
		if title := s.historyCache.titles.Title(path, chat.NativeSessionID); title != "" {
			snapshot.Title = title
		}
	}
	return snapshot, nil
}

func (s *Store) readHistorySnapshot(chat chatmodel.Session, file *nativeHistory) (chatmodel.Session, error) {
	chat.Turns = []chatmodel.Turn{}
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: string(chat.Backend) + "-native"}
	chat.History = nil
	transcripts, err := s.historyCache.transcripts(file)
	if err != nil {
		return chat, err
	}
	if file.title != "" {
		chat.Title = file.title
	}
	for i, native := range file.index {
		transcript := transcripts[i]
		parts := make([]chatmodel.Part, 0, len(transcript.Parts))
		for _, part := range transcript.Parts {
			if part.Role == "user" && part.Kind == "context" {
				continue
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 && !native.HasUserInput() {
			continue
		}
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
		native.Attachments = append([]chatmodel.Attachment(nil), current.Attachments...)
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
