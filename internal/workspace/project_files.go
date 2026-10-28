package workspace

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

const maxFilePreviewBytes = 512 * 1024
const maxDirectoryEntries = 2000

type projectFileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size,omitempty"`
}

func protectedProjectPath(path string) bool {
	for _, name := range strings.Split(filepath.ToSlash(path), "/") {
		name = strings.ToLower(name)
		switch name {
		case ".git", ".spool", ".codex", ".claude", ".ssh", ".aws", ".gnupg", ".npmrc", ".netrc", ".pypirc", ".git-credentials":
			return true
		}
		if name == ".env" || strings.HasPrefix(name, ".env.") {
			return true
		}
	}
	return false
}

// os.Root confines every actual open, including symlink resolution, to the
// authorized project. Absolute Markdown paths are converted, never served as URLs.
func projectFilePath(root, path string) (string, error) {
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", os.ErrPermission
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(path) {
		// macOS commonly records /var or /tmp while the project uses /private/.
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		path, err = filepath.Rel(canonicalRoot, resolved)
		if err != nil {
			return "", os.ErrPermission
		}
	}
	if path == "" {
		path = "."
	}
	path = filepath.Clean(path)
	if !filepath.IsLocal(path) || protectedProjectPath(path) {
		return "", os.ErrPermission
	}
	// Check the resolved name as well so an in-project alias cannot expose
	// protected configuration or application/session databases.
	resolved, err := filepath.EvalSymlinks(filepath.Join(canonicalRoot, path))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || !filepath.IsLocal(relative) || protectedProjectPath(relative) {
		return "", os.ErrPermission
	}
	return path, nil
}

func (a *App) projectFileRoute(w http.ResponseWriter, r *http.Request, user, route string) {
	parts := strings.Split(strings.TrimPrefix(route, "/api/projects/"), "/")
	if len(parts) != 2 || !chatmodel.ValidID(parts[0]) || (parts[1] != "files" && parts[1] != "file") || (r.Method != "GET" && r.Method != "HEAD") || !a.store.CanAccessProject(user, parts[0]) {
		apiError(w, 404, "Project files are unavailable")
		return
	}
	project, err := a.store.Project(parts[0])
	if err != nil {
		apiError(w, 404, "Project files are unavailable")
		return
	}
	path, err := projectFilePath(project.Path, r.URL.Query().Get("path"))
	if err != nil {
		projectFileError(w, err)
		return
	}
	root, err := os.OpenRoot(project.Path)
	if err != nil {
		apiError(w, 404, "Project files are unavailable")
		return
	}
	defer root.Close()
	initial, err := root.Stat(path)
	if err != nil {
		projectFileError(w, err)
		return
	}
	if !initial.IsDir() && !initial.Mode().IsRegular() {
		apiError(w, 400, "Choose a regular file")
		return
	}
	f, err := root.Open(path)
	if err != nil {
		projectFileError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		projectFileError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if parts[1] == "files" {
		if !info.IsDir() {
			apiError(w, 400, "Choose a directory")
			return
		}
		entries, err := f.ReadDir(maxDirectoryEntries + 1)
		if err != nil && err != io.EOF {
			projectFileError(w, err)
			return
		}
		truncated := len(entries) > maxDirectoryEntries
		if truncated {
			entries = entries[:maxDirectoryEntries]
		}
		files := make([]projectFileEntry, 0, len(entries))
		for _, entry := range entries {
			relative := filepath.Join(path, entry.Name())
			if protectedProjectPath(relative) || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			stat, err := entry.Info()
			if err != nil || (!stat.IsDir() && !stat.Mode().IsRegular()) {
				continue
			}
			kind := "file"
			if stat.IsDir() {
				kind = "directory"
			}
			files = append(files, projectFileEntry{Name: entry.Name(), Path: filepath.ToSlash(relative), Kind: kind, Size: stat.Size()})
		}
		sort.Slice(files, func(i, j int) bool {
			if files[i].Kind != files[j].Kind {
				return files[i].Kind == "directory"
			}
			return files[i].Name < files[j].Name
		})
		apiJSON(w, 200, map[string]any{"path": filepath.ToSlash(path), "entries": files, "truncated": truncated})
		return
	}
	if !info.Mode().IsRegular() {
		apiError(w, 400, "Choose a regular file")
		return
	}
	if r.URL.Query().Get("download") == "1" || r.URL.Query().Get("image") == "1" {
		disposition, mediaType := "attachment", "application/octet-stream"
		if r.URL.Query().Get("image") == "1" && r.URL.Query().Get("download") != "1" {
			header := make([]byte, 512)
			n, _ := f.Read(header)
			mediaType = http.DetectContentType(header[:n])
			if !safeProjectImage(mediaType) {
				apiError(w, 400, "This file cannot be previewed as an image")
				return
			}
			disposition = "inline"
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				projectFileError(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": info.Name()}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFilePreviewBytes+1))
	if err != nil {
		projectFileError(w, err)
		return
	}
	truncated := len(data) > maxFilePreviewBytes
	if truncated {
		data = data[:maxFilePreviewBytes]
		// Drop only a partial trailing rune, not invalid bytes inside a binary file.
		start := len(data) - 1
		for start > 0 && len(data)-start < utf8.UTFMax && !utf8.RuneStart(data[start]) {
			start--
		}
		if !utf8.FullRune(data[start:]) {
			data = data[:start]
		}
	}
	mediaType := http.DetectContentType(data)
	kind := "binary"
	var content *string
	if safeProjectImage(mediaType) {
		kind = "image"
	} else if utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
		kind = "text"
		text := string(data)
		content = &text
	}
	apiJSON(w, 200, map[string]any{"path": filepath.ToSlash(path), "name": info.Name(), "size": info.Size(), "kind": kind, "media_type": mediaType, "content": content, "truncated": truncated})
}

func safeProjectImage(mediaType string) bool {
	return mediaType == "image/png" || mediaType == "image/jpeg" || mediaType == "image/gif" || mediaType == "image/webp"
}

func projectFileError(w http.ResponseWriter, err error) {
	if os.IsPermission(err) || strings.Contains(fmt.Sprint(err), "escapes from parent") {
		apiError(w, 403, "This file is outside the project or is protected")
	} else {
		apiError(w, 404, "File not found")
	}
}
