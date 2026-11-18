package workspace

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxImageBytes = 16 * 1024 * 1024

var errImagePreview = errors.New("This file cannot be previewed as an image")

// All session image sources use the same bounded decoder and raster allowlist.
// The declared data URL type is not trusted; serve the actual detected type.
func decodeInlineImage(source string) ([]byte, error) {
	header, encoded, ok := strings.Cut(source, ";base64,")
	if !ok || !strings.HasPrefix(header, "data:image/") || len(encoded) > (maxImageBytes+2)/3*4 {
		return nil, errImagePreview
	}
	data, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes || !safeProjectImage(http.DetectContentType(data)) {
		return nil, errImagePreview
	}
	return data, nil
}

func serveImageContent(w http.ResponseWriter, r *http.Request, name, mediaType string, modified time.Time, content io.ReadSeeker) {
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, modified, content)
}

func serveInlineImage(w http.ResponseWriter, r *http.Request, name, source string) {
	data, err := decodeInlineImage(source)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	serveImageContent(w, r, name, http.DetectContentType(data), time.Time{}, bytes.NewReader(data))
}

// File metadata describes both actions so clients need not reconstruct a URL,
// including the currently selected session worktree or public relay mount.
func imageFileMetadata(r *http.Request, path, name, mediaType string, size int64, external bool) map[string]any {
	address := func(mode string) string {
		query := url.Values{"path": {r.URL.Query().Get("path")}}
		if workspace := r.URL.Query().Get("workspace"); workspace != "" {
			query.Set("workspace", workspace)
		}
		query.Set(mode, "1")
		return (&url.URL{Path: r.URL.Path, RawQuery: query.Encode()}).String()
	}
	return map[string]any{"path": path, "name": name, "size": size, "kind": "image", "media_type": mediaType,
		"content": nil, "truncated": false, "external": external, "preview_url": address("image"), "download_url": address("download")}
}
