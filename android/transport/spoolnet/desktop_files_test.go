package spoolnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type desktopSource struct{ io.Reader }

func (s desktopSource) Read(max int) ([]byte, error) {
	buffer := make([]byte, max)
	n, err := s.Reader.Read(buffer)
	if err == io.EOF {
		err = nil
	}
	return buffer[:n], err
}

type desktopSink struct{ io.Writer }

func (s desktopSink) Write(data []byte) error { _, err := s.Writer.Write(data); return err }

func TestDesktopFilesStreamAuthenticatedUploadsAndDownloadsBeyondJSONBudget(t *testing.T) {
	token := strings.Repeat("x", 43)
	payload := bytes.Repeat([]byte{0, 255, 128, 10}, (17<<20)/4)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "files-fixture", Path: "/", Secure: true, HttpOnly: true})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "files-fixture" || r.TLS == nil || r.Host != "example.com" || r.Header.Get("Origin") != "https://example.com" || r.Header.Get("X-UI-Language") != "zh-CN" {
			t.Error("shared folder lost the exact-origin authenticated transport")
		}
		if r.URL.Query().Get("token") != token || r.URL.Query().Get("path") != "中文 & ?/Reports" || len(r.URL.Query()["token"]) != 1 {
			t.Error("shared-folder identity or path was corrupted", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/desktop/view/files/list":
			io.WriteString(w, `{"path":"中文 & ?/Reports","entries":[]}`)
		case "/api/desktop/view/files/upload":
			if r.Method != "PUT" || r.Header.Get("X-Voxl-Upload") != "1" || r.Header.Get("X-CSRF-Token") != "csrf-fixture" || r.Header.Get("Content-Type") != "application/octet-stream" || r.URL.Query().Get("name") != "a&token=other.bin" {
				t.Error("upload lost method, CSRF, or escaped filename")
			}
			body, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(body, payload) {
				t.Error("large binary upload changed")
			}
			io.WriteString(w, `{"path":"saved.bin"}`)
		case "/api/desktop/view/files/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(payload)
		default:
			t.Error("unexpected shared-folder API", r.URL.Path)
		}
	})
	if _, err := client.Request("POST", "/api/login", `{}`, "", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	files, err := client.OpenDesktopFiles(token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.Close)
	listing, err := files.List("中文 & ?/Reports", "zh-CN")
	if err != nil || !strings.Contains(listing, `"entries":[]`) {
		t.Fatal("could not list shared folder", err)
	}
	if err := files.Upload("中文 & ?/Reports", "a&token=other.bin", "csrf-fixture", "zh-CN", desktopSource{bytes.NewReader(payload)}); err != nil {
		t.Fatal(err)
	}
	var downloaded bytes.Buffer
	if err := files.Download("中文 & ?/Reports", "zh-CN", desktopSink{&downloaded}); err != nil || !bytes.Equal(downloaded.Bytes(), payload) {
		t.Fatal("large binary download changed", err)
	}
}

func TestDesktopFilesRejectUnsafePathsBeforeDialingAndKeepViewTokensPrivate(t *testing.T) {
	var calls atomic.Int32
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) })
	for _, token := range []string{"", "short", strings.Repeat("x", 43) + "&foreign=1"} {
		if _, err := client.OpenDesktopFiles(token); err == nil {
			t.Fatal("accepted invalid token")
		}
	}
	files, _ := client.OpenDesktopFiles(strings.Repeat("x", 43))
	t.Cleanup(files.Close)
	for _, path := range []string{"/etc", "../secret", "a/../secret", `a\secret`, "a\x00b", "a//b", "a/", "./a"} {
		if _, err := files.List(path, "en"); err == nil {
			t.Errorf("accepted unsafe path %q", path)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\nb", strings.Repeat("x", 256)} {
		if err := files.Upload("", name, "csrf", "en", desktopSource{strings.NewReader("fixture")}); err == nil {
			t.Errorf("accepted unsafe filename %q", name)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe paths reached the network")
	}
	if _, err := files.List("", "en"); err == nil || strings.Contains(err.Error(), strings.Repeat("x", 43)) || !strings.Contains(err.Error(), "403") {
		t.Fatal("forbidden request was hidden or leaked the token", err)
	}
	files.Close()
	if _, err := files.List("", "en"); err == nil || calls.Load() != 1 {
		t.Fatal("closed handle reached network", err)
	}
}

func TestDesktopFilesCancelInFlightRequestsAndAllowRetryWithoutReconnecting(t *testing.T) {
	started := make(chan struct{}, 2)
	stopped := make(chan struct{}, 2)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		stopped <- struct{}{}
	})
	files, _ := client.OpenDesktopFiles(strings.Repeat("x", 43))
	t.Cleanup(files.Close)
	for _, cancel := range []func(){files.Cancel, files.Close} {
		result := make(chan error, 1)
		go func() { _, err := files.List("", "en"); result <- err }()
		receiveDesktop(t, started)
		cancel()
		if err := receiveDesktop(t, result); err == nil {
			t.Fatal("cancelled request succeeded")
		}
		receiveDesktop(t, stopped)
	}
}

type failedDesktopSource struct{}

func (failedDesktopSource) Read(int) ([]byte, error) { return nil, errors.New("document read failed") }

type failedDesktopSink struct{}

func (failedDesktopSink) Write([]byte) error { return errors.New("document write failed") }

func TestDesktopFilesReportDocumentErrorsAndClientClosure(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return
			}
		}
		w.Write([]byte("fixture"))
	})
	files, _ := client.OpenDesktopFiles(strings.Repeat("x", 43))
	t.Cleanup(files.Close)
	if err := files.Upload("", "fixture", "csrf", "en", failedDesktopSource{}); err == nil {
		t.Fatal("provider read error was lost")
	}
	if err := files.Download("fixture", "en", failedDesktopSink{}); err == nil {
		t.Fatal("provider write error was lost")
	}
	client.cancel()
	if _, err := files.List("", "en"); !errors.Is(files.ctx.Err(), context.Canceled) || err == nil {
		t.Fatal("client closure did not close shared-folder handle")
	}
}
