package workspace

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	_ "golang.org/x/image/webp"

	chatmodel "spool/internal/model"
)

const (
	maxAttachments     = 6
	maxAttachmentBytes = 4 * 1024 * 1024
	maxUploadBytes     = 16 * 1024 * 1024
)

type uploadedAttachment struct {
	chatmodel.Attachment
	Data []byte
}

func readAttachment(part *multipart.Part) (uploadedAttachment, error) {
	name := strings.TrimSpace(strings.ReplaceAll(part.FileName(), "\\", "/"))
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "" || len(name) > 240 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return uploadedAttachment{}, fmt.Errorf("Invalid attachment filename")
	}
	data, err := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxAttachmentBytes {
		return uploadedAttachment{}, fmt.Errorf("Each attachment must be nonempty and at most 4 MB")
	}
	mediaType := http.DetectContentType(data)
	if strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(part.Header.Get("Content-Type"), "image/") {
		config, format, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 25_000_000 {
			return uploadedAttachment{}, fmt.Errorf("Images must be valid PNG, JPEG, GIF or WebP, up to 25 megapixels")
		}
		switch format {
		case "png", "jpeg", "gif", "webp":
			mediaType = "image/" + format
		default:
			return uploadedAttachment{}, fmt.Errorf("Images must be valid PNG, JPEG, GIF or WebP, up to 25 megapixels")
		}
	}
	return uploadedAttachment{Attachment: chatmodel.Attachment{ID: chatmodel.NewID(), Name: name, MediaType: mediaType, Size: len(data)}, Data: data}, nil
}

func (a *App) attachmentResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session) {
	id := r.URL.Query().Get("id")
	a.filesMu.Lock()
	file, ok := a.uploads[id]
	rootPath := a.filesRoot
	a.filesMu.Unlock()
	if !ok || file.chatID != chat.ID {
		apiError(w, 404, "Attachment not found")
		return
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		apiError(w, 404, "Attachment not found")
		return
	}
	defer root.Close()
	relative, err := filepath.Rel(rootPath, file.file.Path)
	if err != nil {
		apiError(w, 404, "Attachment not found")
		return
	}
	f, err := root.Open(relative)
	if err != nil {
		apiError(w, 404, "Attachment not found")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil || len(data) > maxAttachmentBytes {
		apiError(w, 404, "Attachment not found")
		return
	}
	name, mediaType := file.file.Name, file.file.MediaType
	disposition := "attachment"
	if strings.HasPrefix(mediaType, "image/") {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}

type temporaryAttachment struct {
	chatID string
	file   chatmodel.AgentAttachment
}

// Uploads live only in private temporary files for this process. Native
// sessions own any historical image/content; old files are not rematerialized.
func (a *App) materializeAttachments(chatID string, files []uploadedAttachment) ([]chatmodel.AgentAttachment, error) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	if a.uploads == nil {
		a.uploads = make(map[string]temporaryAttachment)
	}
	var attachments []chatmodel.AgentAttachment
	for _, upload := range files {
		file := chatmodel.AgentAttachment{Attachment: upload.Attachment}
		data := upload.Data
		var err error
		if a.filesRoot == "" {
			a.filesRoot, err = os.MkdirTemp("", "spool-attachments-")
			if err != nil {
				return nil, err
			}
		}
		root, err := os.OpenRoot(a.filesRoot)
		if err != nil {
			return nil, err
		}
		directory := filepath.Join(chatID, file.ID)
		if err := root.MkdirAll(directory, 0700); err != nil {
			root.Close()
			return nil, err
		}
		relative := filepath.Join(directory, file.Name)
		file.Path = filepath.Join(a.filesRoot, relative)
		file.Size = len(data)
		// Replace atomically: a tool may have edited or removed its temporary copy.
		tmpName := filepath.Join(directory, ".upload-"+chatmodel.NewID())
		tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			root.Close()
			return nil, err
		}
		_, writeErr := tmp.Write(data)
		closeErr := tmp.Close()
		if writeErr == nil && closeErr == nil {
			err = root.Rename(tmpName, relative)
		} else {
			err = fmt.Errorf("could not materialize attachment")
		}
		if err != nil {
			_ = root.Remove(tmpName)
			root.Close()
			return nil, err
		}
		root.Close()
		a.uploads[file.ID] = temporaryAttachment{chatID, file}
		attachments = append(attachments, file)
	}
	return attachments, nil
}
