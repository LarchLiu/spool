package workspace

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	chatmodel "spool/internal/model"
)

const maxProjectUploadBytes = 64 << 20

// Stage inside the destination, then publish without replacing existing files.
// The pinned Root confines writes even if a symlink is changed during upload.
func (a *App) serveProjectUpload(w http.ResponseWriter, r *http.Request, directory string, allowed func(string) bool) {
	path, err := projectFilePath(directory, r.URL.Query().Get("path"))
	if err != nil {
		projectFileError(w, err)
		return
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		projectFileError(w, err)
		return
	}
	defer root.Close()
	info, err := root.Stat(path)
	if err != nil {
		projectFileError(w, err)
		return
	}
	if !info.IsDir() {
		apiError(w, 400, "Choose a directory")
		return
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(directory, path))
	if err != nil || allowed != nil && !allowed(resolved) {
		projectFileError(w, os.ErrPermission)
		return
	}
	destination, err := root.OpenRoot(path)
	if err != nil {
		projectFileError(w, err)
		return
	}
	defer destination.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectUploadBytes+65536)
	reader, err := r.MultipartReader()
	if err != nil {
		apiError(w, 400, "Invalid file upload")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		apiError(w, 400, "Invalid file upload")
		return
	}
	defer part.Close()
	// Read the unmodified name: multipart.FileName silently strips traversal.
	_, parameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	name := parameters["filename"]
	if err != nil || part.FormName() != "file" || name == "" || name == "." || name == ".." || !filepath.IsLocal(name) || strings.ContainsAny(name, "/\\\x00\r\n") || protectedProjectPath(name) {
		apiError(w, 400, "Invalid upload filename")
		return
	}
	staging := ".spool-upload-" + chatmodel.NewID()
	f, err := destination.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		projectFileError(w, err)
		return
	}
	defer destination.Remove(staging)
	size, copyErr := io.Copy(f, io.LimitReader(part, maxProjectUploadBytes+1))
	closeErr := f.Close()
	if size > maxProjectUploadBytes {
		apiError(w, 413, "Each uploaded file must be at most 64 MB")
		return
	}
	if copyErr != nil || closeErr != nil {
		apiError(w, 400, "Could not save uploaded file")
		return
	}
	if _, err = reader.NextPart(); err != io.EOF {
		apiError(w, 400, "Upload one file per request")
		return
	}
	if err = r.Context().Err(); err != nil {
		return
	}
	if err = destination.Link(staging, name); os.IsExist(err) {
		apiError(w, 409, "A file with this name already exists")
		return
	} else if err != nil {
		projectFileError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	apiJSON(w, 201, projectFileEntry{Name: name, Path: filepath.ToSlash(filepath.Join(path, name)), Kind: "file", Size: size})
}
