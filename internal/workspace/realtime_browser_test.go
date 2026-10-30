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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"

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
			localSessionHomes(t)
			s := testStore(t)
			alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
			charlie := testUser(t, s, "charlie")
			project := testProject(t, s, alice, bob)
			project.SharedChats = true
			project.SharedLocalChats = true
			writeLocalSession(t, chatmodel.BackendClaude, "browser-local-session", project.Path, 1)
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
			watching, _ := s.CreateChat(bob.ID, project.ID, chatmodel.BackendCodex)
			if _, err = s.db.Exec(`UPDATE sessions SET title=CASE id WHEN ? THEN 'Watching chat' ELSE 'Background chat' END`, watching.ID); err != nil {
				t.Fatal(err)
			}
			cookie, _ := loginRequest(t, a, alice.Username)
			bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
			charlieCookie, charlieCSRF := loginRequest(t, a, charlie.Username)
			probe, err := os.ReadFile("testdata/realtime-browser.js")
			if err != nil {
				t.Fatal(err)
			}
			selectProbe, err := os.ReadFile("testdata/custom-select-browser.js")
			if err != nil {
				t.Fatal(err)
			}
			settingsBridge, err := os.ReadFile("testdata/settings-selects-bridge.js")
			if err != nil {
				t.Fatal(err)
			}
			var events, navigationGets, presencePosts, profileGets atomic.Int64
			reports := make(chan map[string]any, 1)
			var extra *websocket.Conn
			var third *websocket.Conn
			var selectedGroup string
			var actionMu sync.Mutex
			browserProfile := t.TempDir()
			driver := &browserDriver{profile: browserProfile}
			t.Cleanup(driver.close)
			output := filepath.Join("..", "..", "build", "artifacts", "realtime")
			if err := os.MkdirAll(output, 0755); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/smoke/browser":
					var commands []browserCommand
					if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&commands); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					for _, command := range commands {
						switch command.Method {
						case "Input.dispatchMouseEvent", "Input.insertText", "Input.imeSetComposition", "Emulation.setDeviceMetricsOverride", "Emulation.clearDeviceMetricsOverride", "Page.captureScreenshot":
						default:
							http.Error(w, "unsupported browser command", 400)
							return
						}
					}
					ctx, done := context.WithTimeout(r.Context(), 5*time.Second)
					defer done()
					result, err := driver.run(ctx, commands)
					if err == nil && len(commands) > 0 && commands[len(commands)-1].Method == "Page.captureScreenshot" {
						var image struct{ Data string }
						err = json.Unmarshal(result, &image)
						if err == nil {
							var data []byte
							data, err = base64.StdEncoding.DecodeString(image.Data)
							if err == nil {
								name := "chat-window"
								if r.URL.Query().Get("capture") == "group" {
									name = "chat-group"
								} else if r.URL.Query().Get("capture") == "header" {
									name = "chat-header"
								} else if r.URL.Query().Get("capture") == "unread" {
									name = "chat-unread"
								}
								err = os.WriteFile(filepath.Join(output, fmt.Sprintf("%s-%d.png", name, width)), data, 0644)
							}
						}
						result = json.RawMessage(`{}`)
					}
					if err != nil {
						http.Error(w, err.Error(), 500)
					} else {
						w.Header().Set("Content-Type", "application/json")
						w.Write(result)
					}
					return
				case "/smoke/login":
					http.SetCookie(w, cookie)
					http.Redirect(w, r, "/", http.StatusFound)
					return
				case "/smoke/probe.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprintf(w, "window.smokeWidth = %d;\n", width)
					w.Write(selectProbe)
					w.Write(probe)
					return
				case "/smoke/settings-bridge.js":
					w.Header().Set("Content-Type", "text/javascript")
					w.Write(settingsBridge)
					return
				case "/smoke/settings":
					recorder := httptest.NewRecorder()
					request := r.Clone(r.Context())
					request.URL.Path = "/desktop.html"
					a.ServeHTTP(recorder, request)
					w.Header().Set("Content-Type", "text/html")
					w.Write(bytes.Replace(recorder.Body.Bytes(), []byte("<head>"), []byte(`<head><script src="/smoke/settings-bridge.js"></script>`), 1))
					return
				case "/smoke/report":
					var report map[string]any
					if json.NewDecoder(r.Body).Decode(&report) == nil {
						reports <- report
					}
					w.WriteHeader(http.StatusNoContent)
					return
				case "/smoke/state":
					apiJSON(w, 200, map[string]int64{"events": events.Load(), "navigationGets": navigationGets.Load(), "presencePosts": presencePosts.Load(), "profileGets": profileGets.Load()})
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
					case "profile-name":
						_, err = s.saveUserWithPasswordCost(UserInput{ID: bob.ID, Username: bob.Username, Nickname: "Robert"}, bcrypt.MinCost)
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
						if third != nil {
							third.CloseNow()
						}
					case "third-join":
						ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
						origin := "http://" + r.Host
						third, _, err = websocket.Dial(ctx, origin+"/api/events", &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {charlieCookie.String()}, "Origin": {origin}}})
						if err == nil {
							body, _ := json.Marshal(realtimeInput{Type: "hello", CSRF: charlieCSRF, Visible: true})
							err = third.Write(ctx, websocket.MessageText, body)
						}
						done()
					case "selected-invited":
						if groups := a.directMessages.groupsFor(charlie.ID); len(groups) != 1 || groups[0].ID != selectedGroup {
							err = fmt.Errorf("selected group invitation missing")
						}
					case "direct-send", "group-send":
						ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
						input := realtimeInput{Type: "direct_message", RequestID: "browser-inbound", To: alice.ID, Text: "Hello from Bob <script>not executable</script>", Visible: true}
						if r.URL.Query().Get("step") == "group-send" {
							input.Type, input.To, input.Text = "group_message", onlineChatRoom, "Hello group from Bob"
						}
						body, _ := json.Marshal(input)
						err = extra.Write(ctx, websocket.MessageText, body)
						done()
					case "direct-received", "group-received", "selected-received":
						ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
						kind, target, text := "direct_message", bob.ID, "Browser reply"
						if r.URL.Query().Get("step") == "group-received" {
							kind, target, text = "group_message", onlineChatRoom, "Group browser reply"
						}
						if r.URL.Query().Get("step") == "selected-received" {
							kind, target, text = "group_message", "", "Selected browser secret"
						}
						for {
							var data []byte
							_, data, err = extra.Read(ctx)
							if err != nil {
								break
							}
							var event realtimeMessage
							if err = json.Unmarshal(data, &event); err != nil {
								break
							}
							if event.Type == kind {
								var message directMessage
								if err = json.Unmarshal(event.Data, &message); err != nil {
									break
								}
								if message.From == alice.ID && (message.To == target || target == "" && strings.HasPrefix(message.To, "group-")) && message.Text == text {
									if target == "" {
										selectedGroup = message.To
									}
									break
								}
							}
						}
						done()
					case "rename":
						_, err = s.db.Exec(`UPDATE sessions SET title='Changed while disconnected',updated_at=updated_at+1 WHERE id=?`, background.ID)
						s.notify(err, changeNavigation)
					case "readonly", "control":
						access := "control"
						if r.URL.Query().Get("step") == "readonly" {
							access = "read-only"
						}
						_, err = s.db.Exec(`INSERT INTO project_member_access(project_id,user_id,access) VALUES(?,?,?) ON CONFLICT(project_id,user_id) DO UPDATE SET access=excluded.access`, project.ID, alice.ID, access)
						s.notify(err, changeNavigation)
					case "ownreadonly", "owncreate":
						access := "create"
						if r.URL.Query().Get("step") == "ownreadonly" {
							access = "read-only"
						}
						_, err = s.db.Exec(`INSERT INTO project_member_own_access(project_id,user_id,access) VALUES(?,?,?) ON CONFLICT(project_id,user_id) DO UPDATE SET access=excluded.access`, project.ID, alice.ID, access)
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
				case "/api/profiles":
					profileGets.Add(1)
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
				if third != nil {
					third.CloseNow()
				}
				actionMu.Unlock()
				a.Close()
				server.Close()
			})
			log, err := os.Create(filepath.Join(output, fmt.Sprintf("browser-%d.log", width)))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+browserProfile, fmt.Sprintf("--window-size=%d,1000", width), server.URL+"/smoke/login")
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			select {
			case report := <-reports:
				data, _ := json.MarshalIndent(report, "", "  ")
				os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-%d.json", width)), data, 0644)
				if report["error"] != nil {
					t.Fatalf("browser probe failed: %s", data)
				}
				if report["viewportWidth"] != float64(width) {
					t.Errorf("viewport width = %v, want %d", report["viewportWidth"], width)
				}
				for _, check := range []string{"chatInitialFocus", "chatNonmodal", "chatDrag", "chatResize", "chatEdges", "chatBounds", "chatKeyboard", "chatViewport", "chatRetained", "chatSendLayout", "chatEscape", "chatDoesNotStealFocus", "chatConversationKeyboard", "chatLongDraft", "chatSplitLayout", "chatMessageAvatars", "chatStableSidebar", "chatGroupAvatars", "chatPresence", "chatOnlineHeader", "chatUnreadHeader"} {
					if report[check] != true {
						t.Errorf("%s failed: %s", check, data)
					}
				}
				for _, check := range []string{"selectModal", "selectSettingsLanguage", "selectModelEffort", "selectAgentScoped", "selectAutomationSaved", "selectDisabled"} {
					if report[check] != true {
						t.Errorf("%s failed: %s", check, data)
					}
				}
				for _, check := range []string{"tailnetExplicitRestart", "tailnetMissingLoginLink", "tailnetLoginFeedback", "tailnetLoginNonBlocking", "networkProxySettings", "tailnetBrowserLogin", "tailnetSecretInput", "tailnetKeyRegistration", "tailnetKeyNonBlocking", "tailnetConnected", "tailnetPublicOriginIndependent", "tailnetCopyURL", "settingsCopyStable", "settingsPollingStable", "settingsDraftRetained"} {
					if report[check] != true {
						t.Errorf("tailnet settings check failed: %s = %v", check, report[check])
					}
				}
				for _, check := range []string{"localIcon", "controlsUpdated", "ownControlsUpdated", "segmentedStyles", "actorStack", "activeUnchanged", "avatarUpdated", "sharedAvatars", "staleProfilesIgnored", "nameUpdated", "presenceJoined", "presenceLeft", "reconnected", "revoked", "directUnread", "directSent", "directLayout", "directOffline", "directNoReplay", "groupSent", "groupLayout", "groupNoReplay", "selectedCreated", "selectedInvited", "selectedLayout", "selectedClosed", "draftRetained", "unicodeComposer", "imeComposer", "closedGroupAccessible"} {
					if report[check] != true {
						t.Errorf("%s failed: %s", check, data)
					}
				}
				navCount, _ := report["navigationGets"].(float64)
				eventCount, _ := report["events"].(float64)
				profileCount, _ := report["profileGets"].(float64)
				if report["error"] != nil || report["overflow"] != false || report["presencePosts"] != float64(0) || navCount > 2 || eventCount < 3 || profileCount > 3 {
					t.Errorf("unexpected browser state: %s", data)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("browser did not report; inspect", log.Name())
			}
		})
	}
}
