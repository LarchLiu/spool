package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"

	"spool/internal/workspace"
)

// The release probe never opens a caller-supplied database, probes an agent,
// checks for updates, or takes the installed desktop's single-instance lock.
func smokeData() (string, error) {
	root, err := os.MkdirTemp("", "spool-release-smoke-")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) { os.RemoveAll(root); return "", err }
	for _, key := range []string{"SPOOL_CODEX_BIN", "SPOOL_CLAUDE_BIN"} {
		if err := os.Setenv(key, filepath.Join(root, "no-agent-executable")); err != nil {
			return fail(err)
		}
	}
	store, err := workspace.OpenStore(root)
	if err != nil {
		return fail(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		store.Close()
		return fail(err)
	}
	settings := workspace.DefaultSettings()
	settings.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	err = store.SaveSettings(settings)
	closeErr := store.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fail(err)
	}
	return root, nil
}

func probeWeb(ctx context.Context, address string) error {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	for _, page := range []struct{ path, contains string }{
		{"/healthz", `"ok":true`}, {"/", "<title>Spool"}, {"/manifest.webmanifest", `"Spool"`},
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+page.path, nil)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || !bytes.Contains(data, []byte(page.contains)) {
			return fmt.Errorf("smoke probe failed for %s (HTTP %d): %v", page.path, res.StatusCode, err)
		}
	}
	return nil
}

func (p *panel) smoke(report string, result chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Confirm the native event loop still dispatches after window creation.
	dispatched := make(chan struct{})
	p.window.Update(func() { close(dispatched) })
	var err error
	select {
	case <-dispatched:
		for range 3 {
			if err = probeWeb(ctx, p.service.Status().Address); err != nil {
				break
			}
			time.Sleep(time.Second)
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil {
		data, _ := json.Marshal(map[string]any{"version": mygo.App.Version(), "target": runtime.GOOS + "-" + runtime.GOARCH, "native": true, "web": true})
		err = os.WriteFile(report, data, 0o600)
	}
	result <- err
	mygo.App.Quit()
}
