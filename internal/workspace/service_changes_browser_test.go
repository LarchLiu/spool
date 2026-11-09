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

// Exercise settings independently of chat interactions. All state is a
// synthetic bridge; no developer accounts or settings are read or changed.
func TestServiceChangesBrowser(t *testing.T) {
	testSettingsChangesBrowser(t, "service-changes", `snapshot.section="Service"; snapshot.settings.tsnet_enabled=true; activeSettings=structuredClone(snapshot.settings); snapshot.status.TSNet={State:"Running",URL:"https://spool.example.ts.net"};`)
}

func TestSettingsSavesBrowser(t *testing.T) {
	testSettingsChangesBrowser(t, "settings-saves", `snapshot.section="People";`)
}

func testSettingsChangesBrowser(t *testing.T, name, setup string) {
	t.Helper()
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN for the settings browser checks")
	}
	for _, scenario := range []struct {
		width int
		theme string
	}{{1200, "dark"}, {800, "light"}} {
		t.Run(fmt.Sprintf("%d-%s", scenario.width, scenario.theme), func(t *testing.T) {
			app := NewApp(testStore(t), &fakeRunner{}, "", "")
			t.Cleanup(func() { app.Close() })
			bridge, err := os.ReadFile("testdata/settings-selects-bridge.js")
			if err != nil {
				t.Fatal(err)
			}
			probe, err := os.ReadFile("testdata/" + name + "-browser.js")
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join("..", "..", "build", "artifacts", name)
			if err := os.MkdirAll(output, 0755); err != nil {
				t.Fatal(err)
			}
			driver := &browserDriver{profile: t.TempDir()}
			t.Cleanup(driver.close)
			reports := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/bridge.js":
					w.Header().Set("Content-Type", "text/javascript")
					w.Write(bridge)
					fmt.Fprintf(w, `%s window.spoolDesktop=true; window.mygo.locale="zh-CN"; window.spoolDesktopTheme=%q;`, setup, scenario.theme)
				case "/probe.js":
					w.Header().Set("Content-Type", "text/javascript")
					w.Write(probe)
				case "/report":
					var report map[string]any
					if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
						http.Error(w, "invalid report", 400)
						return
					}
					reports <- report
					w.WriteHeader(204)
				case "/address-qr":
					var address string
					if err := json.NewDecoder(r.Body).Decode(&address); err != nil {
						http.Error(w, "invalid address", 400)
						return
					}
					image, err := AddressQRCode(address)
					if err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(image)
				case "/capture":
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					result, err := driver.run(ctx, []browserCommand{{Method: "Page.captureScreenshot", Params: map[string]any{"format": "png"}}})
					if err == nil {
						var image struct{ Data string }
						err = json.Unmarshal(result, &image)
						if err == nil {
							var data []byte
							data, err = base64.StdEncoding.DecodeString(image.Data)
							if err == nil {
								suffix := ""
								if r.URL.Query().Get("variant") == "qr" {
									suffix = "-qr"
								}
								err = os.WriteFile(filepath.Join(output, fmt.Sprintf("settings-%d-%s%s.png", scenario.width, scenario.theme, suffix)), data, 0644)
							}
						}
					}
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					w.WriteHeader(204)
				case "/desktop.html":
					recorder := httptest.NewRecorder()
					app.ServeHTTP(recorder, r)
					w.Header().Set("Content-Type", "text/html")
					html := bytes.Replace(recorder.Body.Bytes(), []byte("<head>"), []byte(`<head><script src="/bridge.js"></script>`), 1)
					w.Write(bytes.Replace(html, []byte("</body>"), []byte(`<script src="/probe.js"></script></body>`), 1))
				default:
					app.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+driver.profile, fmt.Sprintf("--window-size=%d,900", scenario.width), server.URL+"/desktop.html")
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			select {
			case report := <-reports:
				if report["ok"] != true {
					t.Fatalf("settings probe: %+v", report)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("settings browser did not report")
			}
		})
	}
}
