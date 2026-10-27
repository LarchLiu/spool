package workspace

import (
	"errors"
	"os"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type nativeHistory struct {
	start   int
	backend chatmodel.Backend
	id      string
	path    string
	offset  int64
}

type sessionConversion struct {
	native        agent.ConvertedSession
	source        nativeHistory
	importedBytes int64
}

func (s *Store) nativeHistories(chat chatmodel.Session) ([]nativeHistory, error) {
	rows, err := s.db.Query(`SELECT turn_start,backend,native_session_id,native_session_path,native_offset FROM session_history WHERE session_id=? ORDER BY turn_start`, chat.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nativeHistory
	for rows.Next() {
		var item nativeHistory
		if err := rows.Scan(&item.start, &item.backend, &item.id, &item.path, &item.offset); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Called with the turn-admission/store locks held, after input validation but
// before opening SQLite's transaction. A failed transaction deletes only the
// newly converted file, never the source native session.
func (s *Store) convertSession(chat chatmodel.Session, backend chatmodel.Backend) (sessionConversion, error) {
	var empty sessionConversion
	project, err := s.Project(chat.ProjectID)
	if err != nil {
		return empty, err
	}
	path := chat.NativeSessionPath
	if path == "" {
		path, err = agent.FindSessionTranscript(chat.Backend, chat.NativeSessionID, project.Path)
		if err != nil {
			return empty, err
		}
	}
	histories, err := s.nativeHistories(chat)
	if err != nil {
		return empty, err
	}
	start, offset := 0, int64(0)
	if len(histories) > 0 {
		latest := histories[len(histories)-1]
		start, offset = latest.start, latest.offset
	}
	if start >= len(chat.Turns) {
		return empty, errors.New("invalid native history boundary")
	}
	converted, err := agent.ConvertSession(chat.Backend, backend, path, project.Path)
	if err != nil {
		return empty, err
	}
	info, err := os.Stat(converted.Path)
	if err != nil {
		_ = os.Remove(converted.Path)
		return empty, err
	}
	return sessionConversion{
		native:        converted,
		source:        nativeHistory{start: start, offset: offset, backend: chat.Backend, id: chat.NativeSessionID, path: path},
		importedBytes: info.Size(),
	}, nil
}

func (s *Store) hydrateConvertedSession(chat chatmodel.Session, histories []nativeHistory, directory string) chatmodel.Session {
	merged := chat
	merged.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}, Source: "multi-agent-native"}
	merged.Turns = append([]chatmodel.Turn(nil), chat.Turns...)
	for i, history := range histories {
		end := len(chat.Turns)
		if i+1 < len(histories) {
			end = histories[i+1].start
		} else {
			history.backend, history.id, history.path = chat.Backend, chat.NativeSessionID, chat.NativeSessionPath
		}
		if history.start < 0 || end < history.start || end > len(chat.Turns) {
			return chat
		}
		path := history.path
		if path == "" {
			path, _ = agent.FindSessionTranscript(history.backend, history.id, directory)
		}
		native, err := agent.ParseSessionTranscriptFrom(history.backend, path, history.offset)
		segment := chatmodel.Session{Turns: append([]chatmodel.Turn(nil), chat.Turns[history.start:end]...)}
		if err == nil {
			segment, _ = mergeHistoricalTurns(segment, native)
		}
		base := len(merged.Transcript.Parts)
		for _, part := range segment.Transcript.Parts {
			if !chatmodel.AppendPart(&merged.Transcript, part) {
				break
			}
		}
		merged.Transcript.Cut = merged.Transcript.Cut || native.Cut
		limit := len(merged.Transcript.Parts)
		for j, turn := range segment.Turns {
			if len(segment.Transcript.Parts) == 0 {
				turn.PartStart, turn.InputEnd, turn.PartEnd = 0, 0, 0
			}
			turn.PartStart = clamp(turn.PartStart+base, base, limit)
			turn.InputEnd = clamp(turn.InputEnd+base, turn.PartStart, limit)
			turn.PartEnd = clamp(turn.PartEnd+base, turn.InputEnd, limit)
			merged.Turns[history.start+j] = turn
		}
	}
	merged.Transcript.Captured = len(merged.Transcript.Parts)
	return merged
}
