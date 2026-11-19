package workspace

import (
	"net/http"
	"net/url"
	"strconv"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Native image results carry their pixels in the transcript. Serve them
// on demand through the same chat authorization as tool details, even after the
// original file has been removed. History responses carry only these URLs.
func (a *App) toolImageResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session) {
	if chat.History != nil && chat.History.Error != "" {
		apiError(w, 503, "Native session history is unavailable")
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || index < 0 {
		apiError(w, 400, "Invalid tool image index")
		return
	}
	ref := r.URL.Query().Get("ref")
	for _, turn := range chat.Turns {
		for i := max(0, turn.PartStart); i < min(turn.PartEnd, len(chat.Transcript.Parts)); i++ {
			part := chat.Transcript.Parts[i]
			if !toolPart(part) || toolPartRef(chat.ID, turn, i, part) != ref {
				continue
			}
			images := part.Images
			paths := viewedImagePaths(part.Name, part.Text)
			if len(images) == 0 && turn.NativeHistory && part.Kind == "tool_use" && index < len(paths) && chat.NativeSessionPath != "" {
				images, err = agent.ReadCodexToolImages(r.Context(), chat.NativeSessionPath, nativePartTurnID(turn, part), part.ID)
				if err != nil {
					apiError(w, 503, "Native session history is unavailable")
					return
				}
				if len(images) == 0 {
					fileRequest := r.Clone(r.Context())
					fileRequest.URL.RawQuery = url.Values{"path": {paths[index]}, "image": {"1"}}.Encode()
					a.sessionFilesResponse(w, fileRequest, chat, "file")
					return
				}
			}
			if index >= len(images) {
				break
			}
			serveInlineImage(w, r, "image-"+strconv.Itoa(index+1), images[index])
			return
		}
	}
	apiError(w, 404, "Tool image is no longer available")
}
