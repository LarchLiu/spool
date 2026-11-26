package spoolnet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type projectDownloadSink struct {
	write func([]byte) error
}

func (s projectDownloadSink) Write(data []byte) error { return s.write(data) }

func TestProjectFilesStreamLargeDownloadsWithSessionAndWorkspace(t *testing.T) {
	const size = 70 << 20
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "download-fixture", Path: "/", Secure: true, HttpOnly: true})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "download-fixture" || r.TLS == nil || r.Host != "example.com" ||
			r.Header.Get("Origin") != "https://example.com" || r.Header.Get("X-UI-Language") != "zh-CN" ||
			r.URL.Path != "/api/chats/chat-one/file" || r.URL.Query().Get("workspace") != "/repo/work tree" || r.URL.Query().Get("path") != "/exports/数据.bin" {
			t.Error("download lost authenticated transport or file scope", r.URL)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := bytes.Repeat([]byte{0, 255, 128, 10}, (64<<10)/4)
		for n := 0; n < size/len(chunk); n++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	if _, err := client.Request("POST", "/api/login", `{}`, "", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	files := client.OpenProjectFiles()
	t.Cleanup(files.Close)
	received := 0
	result, err := files.Download("/api/chats/chat-one/file?path=%2Fexports%2F%E6%95%B0%E6%8D%AE.bin&download=1&workspace=%2Frepo%2Fwork%20tree", "zh-CN", projectDownloadSink{func(data []byte) error {
		if len(data) > 256<<10 {
			t.Fatal("unbounded download chunk", len(data))
		}
		for i, value := range data {
			if value != []byte{0, 255, 128, 10}[(received+i)%4] {
				t.Fatal("download bytes changed")
			}
		}
		received += len(data)
		return nil
	}})
	if err != nil || received != size || !strings.Contains(result, `"status":200`) {
		t.Fatal("large download failed", received, result, err)
	}
}

func TestProjectDownloadErrorsCancellationAndPathValidation(t *testing.T) {
	var calls atomic.Int32
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("path") == "denied" {
			w.WriteHeader(403)
			io.WriteString(w, `{"error":"File is protected"}`)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, "first chunk")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	files := client.OpenProjectFiles()
	t.Cleanup(files.Close)
	sink := projectDownloadSink{func([]byte) error { return errors.New("disk full") }}
	for _, path := range []string{"https://elsewhere/api/projects/one/file?path=a&download=1", "/api/projects/one/file?path=a", "/api/projects/one/file?path=a&download=1&path=b", "/api/projects/one/file?path=a&download=1&workspace=/repo", "/api/chats/one/messages?download=1"} {
		if _, err := files.Download(path, "en", sink); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid download reached network")
	}
	result, err := files.Download("/api/projects/one/file?path=denied&download=1", "en", sink)
	if err != nil || !strings.Contains(result, `"status":403`) || !strings.Contains(result, "File is protected") {
		t.Fatal("permission failure lost", result, err)
	}
	if _, err := files.Download("/api/projects/one/file?path=large&download=1", "en", sink); err == nil || err.Error() != "disk full" {
		t.Fatal("write failure hidden", err)
	}
	started := make(chan struct{}, 1)
	resultChan := make(chan error, 1)
	go func() {
		_, err := files.Download("/api/chats/one/file?path=large&download=1", "en", projectDownloadSink{func([]byte) error { started <- struct{}{}; return nil }})
		resultChan <- err
	}()
	receiveDesktop(t, started)
	files.Close()
	if err := receiveDesktop(t, resultChan); err == nil {
		t.Fatal("cancelled download succeeded")
	}
}

func TestProjectFilesStreamMultipartToSelectedWorkspaceWithSessionAndCSRF(t *testing.T) {
	payload := bytes.Repeat([]byte{0, 255, 128, 10}, (17<<20)/4)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "fixture", Path: "/", Secure: true, HttpOnly: true})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "fixture" || r.TLS == nil || r.Host != "example.com" ||
			r.Header.Get("Origin") != "https://example.com" || r.Header.Get("X-CSRF-Token") != "csrf-fixture" || r.Header.Get("X-UI-Language") != "zh-CN" {
			t.Error("upload lost the exact-origin authenticated transport")
		}
		if r.Method != "POST" || r.URL.Path != "/api/chats/chat-one/upload" ||
			r.URL.Query().Get("path") != "中文 & ?/Reports" || r.URL.Query().Get("workspace") != "/repo/work tree" {
			t.Error("upload directory or worktree changed", r.URL)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		data, err := io.ReadAll(part)
		if err != nil || part.FormName() != "file" || part.FileName() != "数据 & test.bin" || !bytes.Equal(data, payload) {
			t.Error("large binary multipart upload changed", err)
		}
		if _, err := reader.NextPart(); err != io.EOF {
			t.Error("upload must contain exactly one file", err)
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"name":"数据 & test.bin","path":"中文 & ?/Reports/数据 & test.bin"}`)
	})
	if _, err := client.Request("POST", "/api/login", `{}`, "", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	files := client.OpenProjectFiles()
	t.Cleanup(files.Close)
	encoded, err := files.Upload("/api/chats/chat-one/upload?path=%E4%B8%AD%E6%96%87%20%26%20%3F%2FReports&workspace=%2Frepo%2Fwork%20tree",
		"数据 & test.bin", "csrf-fixture", "zh-CN", desktopSource{bytes.NewReader(payload)})
	var response apiResponse
	if err != nil || json.Unmarshal([]byte(encoded), &response) != nil || response.Status != 201 || !strings.Contains(response.Body, "数据 & test.bin") {
		t.Fatal("lost upload response", encoded, err)
	}
}

func TestProjectFilesRejectInvalidTargetsAndPreservePermissionAndConflictResponses(t *testing.T) {
	var calls atomic.Int32
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Query().Get("path") == "." {
			w.WriteHeader(403)
			io.WriteString(w, `{"error":"File upload permission is disabled"}`)
		} else {
			w.WriteHeader(409)
			io.WriteString(w, `{"error":"A file with this name already exists"}`)
		}
	})
	files := client.OpenProjectFiles()
	t.Cleanup(files.Close)
	for _, path := range []string{
		"https://elsewhere/api/projects/one/upload?path=.", "/api/projects/one/file?path=.",
		"/api/projects/one/upload", "/api/projects/one/upload?path=", "/api/projects/one/upload?path=..",
		"/api/projects/one/upload?path=a%2F..%2Fb", "/api/projects/one/upload?path=.&path=other",
		"/api/projects/one/upload?path=.&workspace=/repo", "/api/chats/one/upload?path=.&workspace=/a&workspace=/b",
		"/api/projects/one/upload?path=.&extra=1",
	} {
		if _, err := files.Upload(path, "test.txt", "csrf", "en", desktopSource{strings.NewReader("test")}); err == nil {
			t.Errorf("accepted invalid target %q", path)
		}
	}
	for _, name := range []string{"", "..", "a/b", "a\nb", "a\\b"} {
		if _, err := files.Upload("/api/projects/one/upload?path=.", name, "csrf", "en", desktopSource{strings.NewReader("test")}); err == nil {
			t.Errorf("accepted invalid name %q", name)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached network")
	}
	for _, target := range []struct{ path, status string }{{".", "403"}, {"reports", "409"}} {
		response, err := files.Upload("/api/projects/one/upload?path="+target.path, "test.txt", "csrf", "en", desktopSource{strings.NewReader("test")})
		if err != nil || !strings.Contains(response, `"status":`+target.status) {
			t.Fatal("lost permission or conflict response", response, err)
		}
	}
	files.Close()
	if _, err := files.Upload("/api/projects/one/upload?path=.", "test.txt", "csrf", "en", desktopSource{strings.NewReader("test")}); err == nil || calls.Load() != 2 {
		t.Fatal("closed file panel reached network", err)
	}
}

func TestProjectFilesCancelRequestsAndReportProviderFailure(t *testing.T) {
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
		stopped <- struct{}{}
	})
	files := client.OpenProjectFiles()
	t.Cleanup(files.Close)
	if _, err := files.Upload("/api/projects/one/upload?path=.", "test.txt", "csrf", "en", failedDesktopSource{}); err == nil {
		t.Fatal("provider failure was hidden")
	}
	result := make(chan error, 1)
	go func() {
		_, err := files.Upload("/api/projects/one/upload?path=.", "test.txt", "csrf", "en", desktopSource{strings.NewReader("test")})
		result <- err
	}()
	receiveDesktop(t, started)
	files.Close()
	if err := receiveDesktop(t, result); err == nil {
		t.Fatal("closed upload succeeded")
	}
	receiveDesktop(t, stopped)
}
