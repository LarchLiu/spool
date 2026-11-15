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

// Native pages preserve app-server boundaries and cursors. File readers use
// the byte-budget transport window below.
func historyPage(chat chatmodel.Session, before string) (chatmodel.Session, error) {
	if chat.History != nil && chat.History.Native && before == "" {
		return chat, nil
	}
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
	// A natively paged chat holds only its loaded turns; more may exist before.
	upstream := chat.History != nil && chat.History.HasMore
	history := &chatmodel.HistoryState{HasMore: start > 0 || upstream && len(chat.Turns) > 0}
	if chat.History != nil {
		history.Error = chat.History.Error
	}
	if start > 0 {
		// Anchor to the preceding turn, not an unwritten live turn whose native
		// identity may change before the next page is requested.
		history.Before = "after-" + historyCursor(chat.ID, chat.Turns[start-1])
	} else if history.HasMore {
		// Nothing loaded precedes this page: continue before its first turn.
		history.Before = historyCursor(chat.ID, chat.Turns[0])
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
