package workspace

import (
	"errors"
	"strings"

	chatmodel "spool/internal/model"
)

const historyPageBytes = 200_000

var errHistoryCursor = errors.New("invalid history cursor")

func historyCursor(chatID string, turn chatmodel.Turn) string {
	if turn.NativeTurnID != "" {
		return historyTurnID(chatID, turn.NativeTurnID)
	}
	return turn.ID
}

func historyWindow(chat chatmodel.Session) chatmodel.Session {
	page, _ := historyPage(chat, "")
	return page
}

// Pagination is a transport concern only. The store has already decoded and
// cached the entire native session, and live state remains complete in memory.
func historyPage(chat chatmodel.Session, before string) (chatmodel.Session, error) {
	end := len(chat.Turns)
	if before != "" {
		cursor, after := strings.CutPrefix(before, "after-")
		end = -1
		for i, turn := range chat.Turns {
			if cursor == historyCursor(chat.ID, turn) || cursor == turn.ID {
				end = i
				if after {
					end++
				}
				break
			}
		}
		if end < 0 {
			return chatmodel.Session{}, errHistoryCursor
		}
	}
	start, size := end, 0
	for start > 0 && size < historyPageBytes {
		start--
		turn := chat.Turns[start]
		for _, part := range chat.Transcript.Parts[turn.PartStart:turn.PartEnd] {
			size += len(part.Text) + len(part.ImageURL)
		}
	}
	history := &chatmodel.HistoryState{HasMore: start > 0}
	if chat.History != nil {
		history.Error = chat.History.Error
	}
	if start > 0 {
		// Anchor to the preceding turn, not an unwritten live turn whose native
		// identity may change before the next page is requested.
		history.Before = "after-" + historyCursor(chat.ID, chat.Turns[start-1])
	}
	base, limit := 0, 0
	if start < end {
		base, limit = chat.Turns[start].PartStart, chat.Turns[end-1].PartEnd
	}
	chat.Turns = append([]chatmodel.Turn{}, chat.Turns[start:end]...)
	chat.Transcript.Parts = append([]chatmodel.Part{}, chat.Transcript.Parts[base:limit]...)
	for i := range chat.Turns {
		chat.Turns[i].PartStart -= base
		chat.Turns[i].InputEnd -= base
		chat.Turns[i].PartEnd -= base
	}
	chat.Transcript.Captured = len(chat.Transcript.Parts)
	chat.History = history
	return chat, nil
}
