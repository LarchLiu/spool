package spoolnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// ProjectFiles streams document-provider files to a project or selected chat
// workspace. Closing the handle cancels its in-flight request.
type ProjectFiles struct{ transfers *DesktopFiles }

func (c *Client) OpenProjectFiles() *ProjectFiles {
	ctx, cancel := context.WithCancel(c.ctx)
	return &ProjectFiles{transfers: &DesktopFiles{client: c, ctx: ctx, close: cancel}}
}

func validProjectUploadPath(path string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "api" ||
		(parts[2] != "projects" && parts[2] != "chats") || !chatIDPattern.MatchString(parts[3]) || parts[4] != "upload" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["path"]) != 1 || query.Get("path") != "." && !validDesktopFilePath(query.Get("path")) || query.Get("path") == "" {
		return false
	}
	for key, values := range query {
		if len(values) != 1 || key != "path" && (key != "workspace" || parts[2] != "chats") {
			return false
		}
	}
	return true
}

// Upload uses the same single-file multipart contract as the Web project panel.
// Only multipart headers and a bounded chunk are held in memory, including for
// files larger than the JSON attachment transport's budget.
func (f *ProjectFiles) Upload(path, name, csrf, language string, source DesktopFileSource) (string, error) {
	if source == nil || !validProjectUploadPath(path) || !validDesktopFileName(name) {
		return "", errors.New("invalid project upload")
	}
	ctx, done, err := f.transfers.begin()
	if err != nil {
		return "", err
	}
	defer done()
	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	if _, err := writer.CreateFormFile("file", name); err != nil {
		return "", err
	}
	headerSize := envelope.Len()
	if err := writer.Close(); err != nil {
		return "", err
	}
	// One extra byte lets the server reject an oversized document, including
	// providers that cannot report its size before streaming.
	body := io.MultiReader(bytes.NewReader(envelope.Bytes()[:headerSize]),
		io.LimitReader(desktopFileReader{ctx, source}, (64<<20)+1), bytes.NewReader(envelope.Bytes()[headerSize:]))
	resp, err := f.transfers.client.request(ctx, http.MethodPost, path, body, writer.FormDataContentType(), csrf, language)
	if err != nil {
		return "", err
	}
	return f.transfers.client.encodeResponse(resp)
}

func (f *ProjectFiles) Close() { f.transfers.Close() }
