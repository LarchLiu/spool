package main

import (
	"errors"
	"testing"

	"github.com/egoist/mygo"
)

func TestDesktopDownloadReportsActualPathOnlyAfterSuccess(t *testing.T) {
	for _, test := range []struct {
		download             mygo.Download
		state, path, failure string
	}{
		{mygo.Download{URL: "http://localhost/file", Path: "/Downloads/报告 (2).pdf"}, "saved", "/Downloads/报告 (2).pdf", ""},
		{mygo.Download{URL: "http://localhost/file", Path: "/Downloads/partial.pdf", Err: errors.New("disk full")}, "failed", "", "disk full"},
		{mygo.Download{URL: "http://localhost/file"}, "failed", "", ""},
	} {
		got := desktopDownloadResult(&test.download)
		if got.URL != test.download.URL || got.State != test.state || got.Path != test.path || got.Error != test.failure {
			t.Fatalf("download result: %+v", got)
		}
	}
}

func TestDesktopDownloadEarlyConnectionFailureStaysInFilePanel(t *testing.T) {
	address := "http://127.0.0.1:8787/web/server/"
	for _, test := range []struct {
		url string
		ok  bool
	}{
		{"http://127.0.0.1:8787/web/server/api/chats/chat/file?path=report.pdf&download=1", true},
		{"http://127.0.0.1:8787/web/server/api/chats/chat/file?path=report.pdf", false},
		{"http://127.0.0.1:8787/", false},
		{"https://example.com/file?download=1", false},
	} {
		got, ok := desktopDownloadLoadError(address, &mygo.LoadError{URL: test.url, Description: "Connection lost"})
		if ok != test.ok || ok && (got.URL != test.url || got.State != "failed" || got.Error != "Connection lost") {
			t.Fatalf("download connection failure for %s: %+v, %v", test.url, got, ok)
		}
	}
}
