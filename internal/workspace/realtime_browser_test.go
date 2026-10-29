package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Opt-in real-browser smoke; run after building the embedded frontend, with a
// standalone headless Chrome binary and repository-owned TMPDIR.
func TestRealtimeBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN to run the real-browser WebSocket smoke")
	}
	for _, width := range []int{1440, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			s := testStore(t)
			alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
			project := testProject(t, s, alice, bob)
			project.SharedChats = true
			if _, err := s.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			testAgent(t, s)
			a := NewApp(s, &fakeRunner{}, "", "")
			background, _ := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
			turn, err := s.beginTurnBy(background.ID, "baseline", chatmodel.BackendCodex, "", nil, alice.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.FinishTurn(background.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
				t.Fatal(err)
			}
			watching, _ := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
			if _, err = s.db.Exec(`UPDATE sessions SET title=CASE id WHEN ? THEN 'Watching chat' ELSE 'Background chat' END`, watching.ID); err != nil {
				t.Fatal(err)
			}
			cookie, _ := loginRequest(t, a, alice.Username)
			bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
			probe, err := os.ReadFile("testdata/realtime-browser.js")
			if err != nil {
				t.Fatal(err)
			}
			var events, navigationGets, presencePosts atomic.Int64
			reports := make(chan map[string]any, 1)
			var extra *websocket.Conn
			var actionMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/smoke/login":
					http.SetCookie(w, cookie)
					http.Redirect(w, r, "/", http.StatusFound)
					return
				case "/smoke/probe.js":
					w.Header().Set("Content-Type", "text/javascript")
					w.Write(probe)
					return
				case "/smoke/report":
					var report map[string]any
					if json.NewDecoder(r.Body).Decode(&report) == nil {
						reports <- report
					}
					w.WriteHeader(http.StatusNoContent)
					return
				case "/smoke/state":
					apiJSON(w, 200, map[string]int64{"events": events.Load(), "navigationGets": navigationGets.Load(), "presencePosts": presencePosts.Load()})
					return
				case "/smoke/action":
					actionMu.Lock()
					defer actionMu.Unlock()
					var err error
					switch r.URL.Query().Get("step") {
					case "actor":
						_, err = s.beginTurnBy(background.ID, "from bob", chatmodel.BackendCodex, "", nil, bob.ID)
					case "avatar":
						err = s.SaveAvatar(bob.ID, Avatar{Style: "thumbs", Seed: "browser-new"})
					case "join":
						ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
						origin := "http://" + r.Host
						extra, _, err = websocket.Dial(ctx, origin+"/api/events", &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {bobCookie.String()}, "Origin": {origin}}})
						if err == nil {
							body, _ := json.Marshal(realtimeInput{Type: "hello", CSRF: bobCSRF, Visible: true})
							err = extra.Write(ctx, websocket.MessageText, body)
						}
						done()
					case "leave":
						if extra != nil {
							extra.CloseNow()
						}
					case "rename":
						_, err = s.db.Exec(`UPDATE sessions SET title='Changed while disconnected',updated_at=updated_at+1 WHERE id=?`, background.ID)
						s.notify(err, changeNavigation)
					case "revoke":
						project.Members = []string{bob.ID}
						_, err = s.SaveProject(project)
					}
					if err != nil {
						http.Error(w, err.Error(), 500)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
					return
				case "/api/events":
					events.Add(1)
				case "/api/navigation":
					navigationGets.Add(1)
				case "/api/presence":
					presencePosts.Add(1)
				case "/":
					recorder := httptest.NewRecorder()
					a.ServeHTTP(recorder, r)
					for key, value := range recorder.Header() {
						w.Header()[key] = value
					}
					w.Header().Del("Content-Length")
					w.Write(bytes.Replace(recorder.Body.Bytes(), []byte("</body>"), []byte(`<script src="/smoke/probe.js"></script></body>`), 1))
					return
				}
				a.ServeHTTP(w, r)
			}))
			t.Cleanup(func() {
				actionMu.Lock()
				if extra != nil {
					extra.CloseNow()
				}
				actionMu.Unlock()
				a.Close()
				server.Close()
			})
			output := filepath.Join("..", "..", "build", "artifacts", "realtime")
			if err := os.MkdirAll(output, 0755); err != nil {
				t.Fatal(err)
			}
			log, err := os.Create(filepath.Join(output, fmt.Sprintf("browser-%d.log", width)))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+t.TempDir(), fmt.Sprintf("--window-size=%d,1000", width), server.URL+"/smoke/login")
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			select {
			case report := <-reports:
				data, _ := json.MarshalIndent(report, "", "  ")
				os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-%d.json", width)), data, 0644)
				for _, check := range []string{"actorStack", "activeUnchanged", "avatarUpdated", "presenceJoined", "presenceLeft", "reconnected", "revoked"} {
					if report[check] != true {
						t.Errorf("%s failed: %s", check, data)
					}
				}
				navCount, _ := report["navigationGets"].(float64)
				eventCount, _ := report["events"].(float64)
				if report["error"] != nil || report["overflow"] != false || report["presencePosts"] != float64(0) || navCount > 2 || eventCount < 3 {
					t.Errorf("unexpected browser state: %s", data)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("browser did not report; inspect", log.Name())
			}
		})
	}
}
