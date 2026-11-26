package spoolnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// DesktopFileSource reads one bounded chunk from an Android document provider.
// An empty chunk means EOF. File bytes never pass through JSON or base64.
type DesktopFileSource interface {
	Read(maxBytes int) ([]byte, error)
}

// DesktopFileSink writes a chunk to a user-selected Android document.
type DesktopFileSink interface {
	Write(data []byte) error
}

// DesktopFiles owns cancellable HTTP transfers for one explicitly opened view.
// It shares the client's private cookies and exact-origin TLS/relay transport.
type DesktopFiles struct {
	client *Client
	token  string
	ctx    context.Context
	close  context.CancelFunc
	mu     sync.Mutex
	active context.CancelFunc
}

func (c *Client) OpenDesktopFiles(token string) (*DesktopFiles, error) {
	if !desktopTokenPattern.MatchString(token) {
		return nil, errors.New("invalid desktop token")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	return &DesktopFiles{client: c, token: token, ctx: ctx, close: cancel}, nil
}

func validDesktopFilePath(path string) bool {
	if !utf8.ValidString(path) || len(path) > 16384 || strings.ContainsAny(path, "\\\x00") || strings.HasPrefix(path, "/") {
		return false
	}
	if path == "" {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if !validDesktopFileName(part) {
			return false
		}
	}
	return true
}

func validDesktopFileName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) &&
		!strings.ContainsAny(name, "/\\") && strings.IndexFunc(name, unicode.IsControl) < 0
}

func (f *DesktopFiles) begin() (context.Context, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx.Err() != nil || f.active != nil {
		return nil, nil, errors.New("desktop files are closed or busy")
	}
	ctx, cancel := context.WithCancel(f.ctx)
	f.active = cancel
	return ctx, func() {
		cancel()
		f.mu.Lock()
		f.active = nil
		f.mu.Unlock()
	}, nil
}

func (f *DesktopFiles) request(ctx context.Context, operation, path, name, language, csrf string, body io.Reader) (*http.Response, error) {
	if !validDesktopFilePath(path) || operation == "upload" && !validDesktopFileName(name) {
		return nil, errors.New("invalid shared-folder path")
	}
	query := url.Values{"token": {f.token}, "path": {path}}
	method := http.MethodGet
	if operation == "upload" {
		method = http.MethodPut
		query.Set("name", name)
	}
	address, err := f.client.apiURL("/api/desktop/view/files/" + operation + "?" + query.Encode())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, errors.New("could not prepare desktop file request")
	}
	req.Header.Set("Origin", f.client.origin.String())
	req.Header.Set("X-UI-Language", language)
	if method == http.MethodPut {
		req.Header.Set("X-Voxl-Upload", "1")
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := f.client.http.Do(req)
	if err != nil {
		// Transport errors can include the URL's private view token.
		return nil, errors.New("desktop file request interrupted")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("desktop file request failed (%d)", resp.StatusCode)
	}
	f.client.receivedResponse()
	return resp, nil
}

func (f *DesktopFiles) List(path, language string) (string, error) {
	ctx, done, err := f.begin()
	if err != nil {
		return "", err
	}
	defer done()
	resp, err := f.request(ctx, "list", path, "", language, "", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return "", errors.New("could not read shared-folder listing")
	}
	return string(data), nil
}

type desktopFileReader struct {
	ctx    context.Context
	source DesktopFileSource
}

func (r desktopFileReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	data, err := r.source.Read(min(len(p), 256<<10))
	if err != nil {
		return 0, err
	}
	if len(data) > min(len(p), 256<<10) {
		return 0, errors.New("document provider returned an oversized chunk")
	}
	if len(data) == 0 {
		return 0, io.EOF
	}
	return copy(p, data), nil
}

func (f *DesktopFiles) Upload(path, name, csrf, language string, source DesktopFileSource) error {
	if source == nil {
		return errors.New("missing upload document")
	}
	ctx, done, err := f.begin()
	if err != nil {
		return err
	}
	defer done()
	resp, err := f.request(ctx, "upload", path, name, language, csrf, desktopFileReader{ctx, source})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return err
}

func (f *DesktopFiles) Download(path, language string, sink DesktopFileSink) error {
	if sink == nil {
		return errors.New("missing download document")
	}
	ctx, done, err := f.begin()
	if err != nil {
		return err
	}
	defer done()
	resp, err := f.request(ctx, "download", path, "", language, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return streamFileDownload(ctx, resp.Body, sink)
}

func streamFileDownload(ctx context.Context, body io.Reader, sink DesktopFileSink) error {
	buffer := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := body.Read(buffer)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if writeErr := sink.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("file download interrupted")
		}
	}
}

func (f *DesktopFiles) Cancel() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active != nil {
		f.active()
	}
}

func (f *DesktopFiles) Close() { f.close() }
