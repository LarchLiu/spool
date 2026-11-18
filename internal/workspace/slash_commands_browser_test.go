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

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Opt-in check of the rendered menu and execution, using owned provider homes
// and browser profiles. Build the embedded frontend before running it.
func TestSlashCommandsBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN to run the slash command browser check")
	}
	for _, width := range []int{1440, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			localSessionHomes(t)
			s := testStore(t)
			u := testUser(t, s, "alice")
			p := testProject(t, s, u)
			fixture := newAgentFixture(t, chatmodel.BackendClaude)
			skills := []map[string]any{
				{"name": "inspect", "path": filepath.Join(fixture.WorkingDirectory, "skills", "inspect", "SKILL.md"), "enabled": true, "scope": "repo", "description": "Inspect a project", "interface": map[string]any{"displayName": "Inspect project", "shortDescription": "Check project health"}},
				{"name": "docs", "path": filepath.Join(fixture.WorkingDirectory, "skills", "docs", "SKILL.md"), "enabled": true, "scope": "user", "description": "Write and edit documentation", "interface": map[string]any{"displayName": "Documentation"}},
			}
			for _, extra := range []struct{ name, display, description, plugin string }{
				{"blender", "Box Blender", "Build, render, and export 3D models with Blender", ""},
				{"excel", "Excel Live Control", "Control an open or active Microsoft Excel workbook", "Spreadsheets"},
				{"pdf", "PDF", "Read, create, inspect, render, and verify PDF files", "PDF"},
				{"slides", "Presentations", "Create, read, and edit PowerPoint or Google Slides decks", "Presentations"},
				{"skill-creator", "Skill Creator", "Create new skills and improve existing skills", ""},
			} {
				skills = append(skills, map[string]any{"name": extra.name, "path": filepath.Join(fixture.WorkingDirectory, "skills", extra.name, "SKILL.md"), "enabled": true, "scope": "user", "pluginId": extra.plugin, "description": extra.description, "interface": map[string]any{"displayName": extra.display}})
			}
			skillData, _ := json.Marshal(skills)
			if err := os.WriteFile(filepath.Join(fixture.WorkingDirectory, "fixture-skills.json"), skillData, 0600); err != nil {
				t.Fatal(err)
			}
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{chatmodel.BackendCodex: fixture.Binary, chatmodel.BackendClaude: fixture.Binary})
			for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
				var catalog chatmodel.CodexCatalog
				var err error
				if backend == chatmodel.BackendClaude {
					catalog, err = agent.DiscoverClaudeModels(t.Context(), fixture.Binary)
				} else {
					catalog, err = runner.CodexModels(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := s.saveAgent(AgentInfo{Backend: backend, Enabled: true, Installed: true, Catalog: catalog}); err != nil {
					t.Fatal(err)
				}
			}
			a := NewApp(s, runner, "", "")
			chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendClaude)
			cookie, _ := loginRequest(t, a, "alice")
			probe, err := os.ReadFile("testdata/slash-commands-browser.js")
			if err != nil {
				t.Fatal(err)
			}
			reports := make(chan map[string]any, 1)
			profile := t.TempDir()
			driver := &browserDriver{profile: profile}
			t.Cleanup(driver.close)
			output := filepath.Join("..", "..", "build", "artifacts", "slash-commands")
			if err := os.MkdirAll(output, 0755); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/smoke/viewport", "/smoke/capture":
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					command := browserCommand{Method: "Emulation.setDeviceMetricsOverride", Params: map[string]any{"width": width, "height": 1000, "deviceScaleFactor": 1, "mobile": false}}
					if r.URL.Path == "/smoke/capture" {
						command = browserCommand{Method: "Page.captureScreenshot", Params: map[string]any{"format": "png"}}
					}
					result, err := driver.run(ctx, []browserCommand{command})
					if err == nil && r.URL.Path == "/smoke/capture" {
						var image struct{ Data string }
						err = json.Unmarshal(result, &image)
						if err == nil {
							var data []byte
							data, err = base64.StdEncoding.DecodeString(image.Data)
							if err == nil {
								prefix := "commands"
								if r.URL.Query().Get("agent") == "claude" {
									prefix = "claude-commands"
								}
								err = os.WriteFile(filepath.Join(output, fmt.Sprintf("%s-%d.png", prefix, width)), data, 0644)
							}
						}
					}
					if err != nil {
						http.Error(w, err.Error(), 500)
					} else {
						w.WriteHeader(204)
					}
				case "/smoke/login":
					http.SetCookie(w, cookie)
					http.Redirect(w, r, "/?chat="+chat.ID, http.StatusFound)
				case "/smoke/probe.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprintf(w, "window.smokeWidth=%d;\n", width)
					w.Write(probe)
				case "/smoke/report":
					var report map[string]any
					if json.NewDecoder(r.Body).Decode(&report) == nil {
						reports <- report
					}
					w.WriteHeader(http.StatusNoContent)
				case "/":
					recorder := httptest.NewRecorder()
					a.ServeHTTP(recorder, r)
					for key, values := range recorder.Header() {
						w.Header()[key] = values
					}
					w.Header().Del("Content-Length")
					w.Write(bytes.Replace(recorder.Body.Bytes(), []byte("</body>"), []byte(`<script>localStorage.setItem("spool.ui-preferences.v1",JSON.stringify({theme:"dark",language:"en"}));</script><script src="/smoke/probe.js"></script></body>`), 1))
				default:
					a.ServeHTTP(w, r)
				}
			}))
			t.Cleanup(func() { a.Close(); server.Close() })
			log, err := os.Create(filepath.Join(output, fmt.Sprintf("browser-%d.log", width)))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+profile, fmt.Sprintf("--window-size=%d,1000", width), server.URL+"/smoke/login")
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			select {
			case report := <-reports:
				data, _ := json.MarshalIndent(report, "", "  ")
				os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-%d.json", width)), data, 0644)
				if report["error"] != nil || report["complete"] != true {
					for _, entry := range fixtureLog(t, fixture) {
						t.Logf("fixture method: %s", entry["method"])
					}
					t.Fatalf("%s", data)
				}
				for _, name := range []string{"cachedCatalog", "keyboard", "agentSwitch", "bounds", "nativeExecution", "settingsSynced", "noTurns", "draftExecution", "skillList", "skillSelection", "skillExecution", "noBrowserErrors", "claudeList", "claudePresentation", "codexPresentation", "claudeSkillSelection", "claudeSkillExecution", "claudeReviewSelection", "codexReviewSelection"} {
					if report[name] != true {
						t.Errorf("%s failed: %s", name, data)
					}
				}
				found, claudeSkillFound := false, false
				for _, entry := range fixtureLog(t, fixture) {
					if entry["method"] == "user" {
						value := entry["value"].(map[string]any)
						message, _ := value["message"].(map[string]any)
						if message["content"] == "/project:check Check Claude browser selection" {
							claudeSkillFound = true
						}
					}
					if entry["method"] != "turn/start" {
						continue
					}
					value := entry["value"].(map[string]any)
					input := value["params"].(map[string]any)["input"].([]any)
					if len(input) != 2 || input[0].(map[string]any)["text"] != "Check browser selection" {
						t.Fatalf("unexpected skill turn: %#v", input)
					}
					skill := input[1].(map[string]any)
					if skill["type"] != "skill" || skill["name"] != "inspect" || skill["path"] != skills[0]["path"] {
						t.Fatalf("skill selection did not reach native input: %#v", skill)
					}
					found = true
				}
				if !found {
					t.Fatal("browser did not invoke a native Codex skill")
				}
				if !claudeSkillFound {
					t.Fatal("browser did not invoke a native Claude skill")
				}
			case <-time.After(45 * time.Second):
				t.Fatal("slash command browser check timed out")
			}
		})
	}
}
