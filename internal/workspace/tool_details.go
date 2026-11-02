package workspace

import (
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

const toolDetailPageBytes = 32 * 1024

type toolDetailPage struct {
	Ref          string  `json:"ref"`
	Version      string  `json:"version"`
	Text         string  `json:"text"`
	Offset       int     `json:"offset"`
	Next         int     `json:"next"`
	Total        int     `json:"total"`
	Prefix       string  `json:"prefix"`
	Reset        bool    `json:"reset,omitempty"`
	ServerMillis float64 `json:"server_ms,omitempty"`
}

func lazyToolDetails(r *http.Request) bool {
	return r.URL.Query().Get("tool_details") == "lazy" || r.Header.Get("X-Tool-Details") == "lazy"
}

func transportSession(r *http.Request, chat chatmodel.Session) chatmodel.Session {
	if lazyToolDetails(r) {
		return slimToolDetails(chat)
	}
	return chat
}

func transportWindow(r *http.Request, chat chatmodel.Session) chatmodel.Session {
	return transportSession(r, historyWindow(chat))
}

// A cursor covers the already received prefix, not a mutable whole-body
// version. Appends continue from Next; replacements restart at zero explicitly.
func readToolDetailPage(chat chatmodel.Session, ref string, offset int, prefix string) (toolDetailPage, bool) {
	if offset < 0 {
		return toolDetailPage{}, false
	}
	for _, turn := range chat.Turns {
		for i := max(0, turn.PartStart); i < min(turn.PartEnd, len(chat.Transcript.Parts)); i++ {
			part := chat.Transcript.Parts[i]
			if !toolPart(part) || toolPartRef(chat.ID, turn, i, part) != ref {
				continue
			}
			reset := offset > len(part.Text) || offset > 0 && (offset < len(part.Text) && !utf8.RuneStart(part.Text[offset]) || prefix == "" || toolBodyVersion(part.Text[:offset]) != prefix)
			if reset {
				offset = 0
			}
			end := min(len(part.Text), offset+toolDetailPageBytes)
			if end < len(part.Text) {
				for end > offset && !utf8.RuneStart(part.Text[end]) {
					end--
				}
			}
			return toolDetailPage{Ref: ref, Version: summarizeTool(part).Version, Text: part.Text[offset:end], Offset: offset,
				Next: end, Total: len(part.Text), Prefix: toolBodyVersion(part.Text[:end]), Reset: reset}, true
		}
	}
	return toolDetailPage{}, false
}

func (a *App) toolDetailResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, started time.Time) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			apiError(w, 400, "Invalid tool detail cursor")
			return
		}
		offset = value
	}
	if chat.History != nil && chat.History.Error != "" {
		apiError(w, 503, "Native session history is unavailable")
		return
	}
	page, ok := readToolDetailPage(chat, r.URL.Query().Get("ref"), offset, r.URL.Query().Get("prefix"))
	if !ok {
		apiError(w, 404, "Tool detail is no longer available")
		return
	}
	page.ServerMillis = float64(time.Since(started).Microseconds()) / 1000
	w.Header().Set("Server-Timing", "tool-detail;dur="+strconv.FormatFloat(page.ServerMillis, 'f', 3, 64))
	apiJSON(w, 200, page)
}
