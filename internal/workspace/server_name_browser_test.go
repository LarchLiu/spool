package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestServerNameBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN for the server name browser check")
	}
	for _, width := range []int{1440, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			store := testStore(t)
			user := testUser(t, store, "alice")
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { app.Close() })
			cookie, _ := loginRequest(t, app, user.Username)
			probe, err := os.ReadFile("testdata/server-name-browser.js")
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join("..", "..", "build", "artifacts", "server-name")
			if err := os.MkdirAll(output, 0755); err != nil {
				t.Fatal(err)
			}
			driver := &browserDriver{profile: t.TempDir()}
			t.Cleanup(driver.close)
			reports := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/smoke/login":
					http.SetCookie(w, cookie)
					http.Redirect(w, r, "/", http.StatusFound)
				case "/smoke/probe.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprintf(w, "window.smokeWidth = %d;\n", width)
					w.Write(probe)
				case "/smoke/name":
					settings, err := store.Settings()
					if err == nil {
						settings.ServerName = r.URL.Query().Get("name")
						err = store.SaveSettings(settings)
					}
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					w.WriteHeader(204)
				case "/smoke/browser":
					var commands []browserCommand
					if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&commands); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					for _, command := range commands {
						if command.Method != "Emulation.setDeviceMetricsOverride" && command.Method != "Page.captureScreenshot" {
							http.Error(w, "unsupported browser command", 400)
							return
						}
					}
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					result, err := driver.run(ctx, commands)
					if err == nil && len(commands) > 0 && commands[len(commands)-1].Method == "Page.captureScreenshot" {
						var image struct{ Data string }
						err = json.Unmarshal(result, &image)
						if err == nil {
							var data []byte
							data, err = base64.StdEncoding.DecodeString(image.Data)
							if err == nil {
								err = os.WriteFile(filepath.Join(output, fmt.Sprintf("sidebar-%d.png", width)), data, 0644)
							}
						}
						result = json.RawMessage(`{}`)
					}
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.Write(result)
				case "/smoke/report":
					var report map[string]any
					if json.NewDecoder(r.Body).Decode(&report) == nil {
						reports <- report
					}
					w.WriteHeader(204)
				case "/":
					recorder := httptest.NewRecorder()
					app.ServeHTTP(recorder, r)
					for key, values := range recorder.Header() {
						w.Header()[key] = values
					}
					w.Header().Del("Content-Length")
					w.Write(bytes.Replace(recorder.Body.Bytes(), []byte("</body>"), []byte(`<script src="/smoke/probe.js"></script></body>`), 1))
				default:
					app.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+driver.profile, server.URL+"/smoke/login")
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			select {
			case report := <-reports:
				if report["ok"] != true {
					t.Fatalf("server name browser check: %+v", report)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("server name browser did not report")
			}
		})
	}
}
