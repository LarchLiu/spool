package workspace

import (
	"net/http"
	"path/filepath"
	"strings"

	chatmodel "spool/internal/model"
)

func archivedUserImage(chat chatmodel.Session, path string) string {
	for _, part := range chat.Transcript.Parts {
		if part.Role == "user" && part.Kind == "image" && part.FilePath == path && part.Name == filepath.Base(path) && part.ImageURL != "" {
			return part.ImageURL
		}
	}
	return ""
}

// Native history can retain a temporary image path after its upload directory
// is gone. Recover only pixels paired with that exact user attachment, on demand.
func (a *App) archivedUserImageResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, path string) bool {
	if r.URL.Query().Get("office") == "1" || r.URL.Query().Get("preview") == "1" {
		return false
	}
	source := archivedUserImage(chat, path)
	if source == "" && chat.History != nil && chat.History.Native && chat.NativeSessionPath != "" {
		err := a.store.historyCache.read(chat.Backend, chat.NativeSessionPath, func(file *nativeHistory) error {
			complete, err := a.store.readHistorySnapshot(chat, file)
			if err == nil {
				source = archivedUserImage(complete, path)
			}
			return err
		})
		if err != nil {
			apiError(w, 503, "Native session history is unavailable")
			return true
		}
	}
	if source == "" {
		return false
	}
	if r.URL.Query().Get("image") == "1" || r.URL.Query().Get("download") == "1" {
		serveInlineImage(w, r, filepath.Base(path), source)
		return true
	}
	data, err := decodeInlineImage(source)
	if err != nil {
		apiError(w, 400, err.Error())
		return true
	}
	w.Header().Set("Cache-Control", "private, no-store")
	apiJSON(w, 200, imageFileMetadata(r, path, filepath.Base(path), http.DetectContentType(data), int64(len(data)), true))
	return true
}

// Upload IDs are process-local, but the provider records their full path. Match
// the complete chat/id/name layout; a same-name image or arbitrary path is never
// a replacement for an upload whose in-memory registry has gone away.
func uploadedImagePath(chat chatmodel.Session, id string) string {
	for _, part := range chat.Transcript.Parts {
		path := part.FilePath
		parent := filepath.Dir(path)
		chatDirectory := filepath.Dir(parent)
		root := filepath.Base(filepath.Dir(chatDirectory))
		if part.Role == "user" && part.Kind == "image" && part.Name == filepath.Base(path) && filepath.IsAbs(path) &&
			filepath.Base(parent) == id && filepath.Base(chatDirectory) == chat.ID && strings.HasPrefix(root, "spool-attachments-") {
			return path
		}
	}
	return ""
}

func (a *App) archivedUploadResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, id string) bool {
	if !chatmodel.ValidID(id) {
		return false
	}
	snapshot := a.snapshot(chat)
	path := uploadedImagePath(snapshot, id)
	if path == "" && snapshot.History != nil && snapshot.History.Native && chat.NativeSessionPath != "" {
		err := a.store.historyCache.read(chat.Backend, chat.NativeSessionPath, func(file *nativeHistory) error {
			complete, err := a.store.readHistorySnapshot(chat, file)
			if err == nil {
				path = uploadedImagePath(complete, id)
			}
			return err
		})
		if err != nil {
			apiError(w, 503, "Native session history is unavailable")
			return true
		}
	}
	if path == "" {
		return false
	}
	request := r.Clone(r.Context())
	query := request.URL.Query()
	query.Set("path", path)
	query.Set("image", "1")
	request.URL.RawQuery = query.Encode()
	a.sessionFilesResponse(w, request, chat, "file")
	return true
}
