package workspace

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

// Native image results carry their pixels in the transcript. Serve them
// on demand through the same chat authorization as tool details, even after the
// original file has been removed. History responses carry only these URLs.
func (a *App) toolImageResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session) {
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || index < 0 {
		apiError(w, 400, "Invalid tool image index")
		return
	}
	ref := r.URL.Query().Get("ref")
	for _, turn := range chat.Turns {
		for i := max(0, turn.PartStart); i < min(turn.PartEnd, len(chat.Transcript.Parts)); i++ {
			part := chat.Transcript.Parts[i]
			if !toolPart(part) || toolPartRef(chat.ID, turn, i, part) != ref || index >= len(part.Images) {
				continue
			}
			_, encoded, ok := strings.Cut(part.Images[index], ";base64,")
			if !ok {
				break
			}
			const maxImageBytes = 16 * 1024 * 1024
			data, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), maxImageBytes+1))
			mediaType := http.DetectContentType(data)
			if err != nil || len(data) > maxImageBytes || !safeProjectImage(mediaType) {
				apiError(w, 400, "This tool result cannot be previewed as an image")
				return
			}
			w.Header().Set("Content-Type", mediaType)
			w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "image-" + strconv.Itoa(index+1)}))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
			w.Header().Set("Cache-Control", "private, no-store")
			http.ServeContent(w, r, "image", time.Time{}, bytes.NewReader(data))
			return
		}
	}
	apiError(w, 404, "Tool image is no longer available")
}
