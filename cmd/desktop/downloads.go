package main

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo"
)

type desktopDownload struct {
	URL   string `json:"url"`
	State string `json:"state"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

var desktopDownloadChanged = mygo.NewEvent[desktopDownload]("spool:download")

func observeDesktopDownloads(page *mygo.Page) {
	page.OnWillDownload(func(e *mygo.DownloadEvent) {
		state := desktopDownload{URL: e.URL, State: "downloading"}
		if e.Path == "" {
			state.State = "failed"
		}
		_ = desktopDownloadChanged.EmitPage(page, state)
	})
	page.OnDownloadDone(func(d *mygo.Download) {
		_ = desktopDownloadChanged.EmitPage(page, desktopDownloadResult(d))
	})
}

func desktopDownloadResult(d *mygo.Download) desktopDownload {
	result := desktopDownload{URL: d.URL, State: "saved", Path: d.Path}
	if d.Err != nil || d.Path == "" {
		result.State, result.Path = "failed", ""
		if d.Err != nil {
			result.Error = d.Err.Error()
		}
	}
	return result
}

// A connection can fail before WebView creates its native download object.
// Keep that failure in the file panel instead of replacing the chat page.
func desktopDownloadLoadError(address string, failure *mygo.LoadError) (desktopDownload, bool) {
	u, err := url.Parse(failure.URL)
	if err != nil || !chatNavigationAllowed(address, failure.URL) || !strings.HasSuffix(u.Path, "/file") || u.Query().Get("download") != "1" {
		return desktopDownload{}, false
	}
	return desktopDownload{URL: failure.URL, State: "failed", Error: failure.Description}, true
}
