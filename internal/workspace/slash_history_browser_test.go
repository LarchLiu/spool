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

	chatmodel "spool/internal/model"
)

// The desktop and Web clients share this production page. Export its synthetic
// server response so the Android renderer can verify the same contract.
func TestSlashCommandHistoryBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN to check command history in the browser")
	}
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		for _, width := range []int{1440, 390} {
			t.Run(fmt.Sprintf("%s-%d", backend, width), func(t *testing.T) {
				store := testStore(t)
				owner := testUser(t, store, "command-owner")
				project := testProject(t, store, owner)
				chat := slashHistoryChat(t, store, owner.ID, project.ID, backend)
				loaded, _ := store.Get(chat.ID)
				prompts := slashHistoryPrompts(backend)
				app := NewApp(store, &fakeRunner{}, "", "")
				cookie, _ := loginRequest(t, app, owner.Username)
				output := filepath.Join("..", "..", "build", "artifacts", "slash-history")
				if err := os.MkdirAll(output, 0755); err != nil {
					t.Fatal(err)
				}
				snapshot, _ := json.Marshal(historyWindow(loaded))
				if err := os.WriteFile(filepath.Join(output, string(backend)+".json"), snapshot, 0644); err != nil {
					t.Fatal(err)
				}
				driver := &browserDriver{profile: t.TempDir()}
				t.Cleanup(driver.close)
				reports := make(chan map[string]any, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/smoke/login":
						http.SetCookie(w, cookie)
						http.Redirect(w, r, "/?chat="+chat.ID, http.StatusFound)
					case "/smoke/probe.js":
						w.Header().Set("Content-Type", "text/javascript")
						expected, _ := json.Marshal(prompts)
						fmt.Fprintf(w, "window.commandHistoryPrompts=%s;\n", expected)
						fmt.Fprint(w, slashHistoryBrowserProbe)
					case "/smoke/report":
						var report map[string]any
						if json.NewDecoder(r.Body).Decode(&report) == nil {
							reports <- report
						}
						w.WriteHeader(204)
					case "/smoke/viewport":
						ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
						defer cancel()
						_, err := driver.run(ctx, []browserCommand{{Method: "Emulation.setDeviceMetricsOverride", Params: map[string]any{"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false}}})
						if err != nil {
							http.Error(w, err.Error(), 500)
						} else {
							w.WriteHeader(204)
						}
					case "/smoke/capture":
						ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
						defer cancel()
						result, err := driver.run(ctx, []browserCommand{{Method: "Page.captureScreenshot", Params: map[string]any{"format": "png"}}})
						var image struct{ Data string }
						if err == nil {
							err = json.Unmarshal(result, &image)
						}
						if err == nil {
							var data []byte
							data, err = base64.StdEncoding.DecodeString(image.Data)
							if err == nil {
								err = os.WriteFile(filepath.Join(output, fmt.Sprintf("%s-%d.png", backend, width)), data, 0644)
							}
						}
						if err != nil {
							http.Error(w, err.Error(), 500)
						} else {
							w.WriteHeader(204)
						}
					case "/":
						recorder := httptest.NewRecorder()
						app.ServeHTTP(recorder, r)
						for key, values := range recorder.Header() {
							w.Header()[key] = values
						}
						w.Header().Del("Content-Length")
						w.Write(bytes.Replace(recorder.Body.Bytes(), []byte("</body>"), []byte(`<script>localStorage.setItem("spool.ui-preferences.v1",JSON.stringify({theme:"light",language:"en"}));</script><script src="/smoke/probe.js"></script></body>`), 1))
					default:
						app.ServeHTTP(w, r)
					}
				}))
				t.Cleanup(func() { _ = app.Close(); server.Close() })
				log, err := os.Create(filepath.Join(output, fmt.Sprintf("%s-%d.log", backend, width)))
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-extensions", "--remote-debugging-port=0", "--user-data-dir="+driver.profile, fmt.Sprintf("--window-size=%d,900", width), server.URL+"/smoke/login")
				command.Stdout, command.Stderr = log, log
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
				select {
				case report := <-reports:
					data, _ := json.MarshalIndent(report, "", "  ")
					_ = os.WriteFile(filepath.Join(output, fmt.Sprintf("%s-%d.json", backend, width)), data, 0644)
					if report["complete"] != true {
						t.Fatalf("%s", data)
					}
				case <-time.After(45 * time.Second):
					t.Fatal("command history browser timed out")
				}
			})
		}
	}
}

const slashHistoryBrowserProbe = `(async()=>{
const report={}, errors=[];
window.addEventListener("error",event=>errors.push(String(event.error||event.message)));
try {
if(!(await fetch("/smoke/viewport")).ok)throw Error("viewport setup failed");
for(let i=0;i<400&&!document.querySelector(".assistant-text")?.textContent;i++)await new Promise(resolve=>setTimeout(resolve,25));
const turns=[...document.querySelectorAll(".turn")], prompts=window.commandHistoryPrompts;
if(turns.length!==prompts.length)throw Error("history lost turns: "+turns.length+"/"+prompts.length);
for(let i=0;i<prompts.length;i++){
if(turns[i].querySelector(".user-bubble-content")?.textContent.trim()!==prompts[i])throw Error("missing command "+prompts[i]);
if(turns[i].querySelector(".assistant-text")?.textContent.trim()!=="Reply to "+prompts[i])throw Error("missing answer "+prompts[i]);
}
if(document.body.textContent.includes("Internal skill instructions")||document.body.textContent.includes("<command-message>"))throw Error("native metadata leaked");
document.querySelector(".turn")?.scrollIntoView();
await Promise.all(document.getAnimations().filter(animation=>animation.effect?.getTiming().iterations!==Infinity).map(animation=>animation.finished.catch(()=>{})));
await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
if(!(await fetch("/smoke/capture")).ok)throw Error("screenshot failed");
if(document.documentElement.scrollWidth>innerWidth+1)throw Error("horizontal overflow");
if(errors.length)throw Error(errors.join("; "));
Object.assign(report,{complete:true,width:innerWidth,turns:turns.length,prompts,commandsAndReplies:true,continuation:true,metadataHidden:true,noOverflow:true});
}catch(error){report.error=String(error);report.body=document.body.innerText.slice(-1500);}
await fetch("/smoke/report",{method:"POST",body:JSON.stringify(report)});
})();`
