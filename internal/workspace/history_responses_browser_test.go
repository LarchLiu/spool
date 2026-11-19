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

// Uses a synthetic native review and production history/rendering paths.
func TestCodexReviewHistoryBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN to run the review history browser check")
	}
	for _, width := range []int{1100, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			localSessionHomes(t)
			store := testStore(t)
			owner := testUser(t, store, "review-owner")
			project := testProject(t, store, owner)
			chat, err := store.CreateChat(owner.ID, project.ID, "codex")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			data, err := os.ReadFile("../agent/testdata/codex-review.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := store.SetNativeSessionPath(chat.ID, "review-thread", path); err != nil {
				t.Fatal(err)
			}
			store.codexHistory.reader = &responseHistory{turns: codexReviewTurns()}
			app := NewApp(store, &fakeRunner{}, "", "")
			cookie, _ := loginRequest(t, app, owner.Username)
			output := filepath.Join("..", "..", "build", "artifacts", "review-history")
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
					http.Redirect(w, r, "/?chat="+chat.ID, http.StatusFound)
				case "/smoke/probe.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprintf(w, "window.smokeWidth=%d;\n", width)
					fmt.Fprint(w, codexReviewHistoryProbe)
				case "/smoke/report":
					var report map[string]any
					if json.NewDecoder(r.Body).Decode(&report) == nil {
						reports <- report
					}
					w.WriteHeader(204)
				case "/smoke/capture":
					ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
					defer cancel()
					commands := []browserCommand{{Method: "Emulation.setDeviceMetricsOverride", Params: map[string]any{"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false}}, {Method: "Page.captureScreenshot", Params: map[string]any{"format": "png"}}}
					result, err := driver.run(ctx, commands)
					var image struct{ Data string }
					if err == nil {
						err = json.Unmarshal(result, &image)
					}
					if err == nil {
						var data []byte
						data, err = base64.StdEncoding.DecodeString(image.Data)
						if err == nil {
							err = os.WriteFile(filepath.Join(output, fmt.Sprintf("review-%d.png", width)), data, 0644)
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
					w.Write(bytes.Replace(recorder.Body.Bytes(), []byte("</body>"), []byte(`<script>localStorage.setItem("spool.ui-preferences.v1",JSON.stringify({theme:"dark",language:"en"}));</script><script src="/smoke/probe.js"></script></body>`), 1))
				default:
					app.ServeHTTP(w, r)
				}
			}))
			t.Cleanup(func() { app.Close(); server.Close() })
			log, err := os.Create(filepath.Join(output, fmt.Sprintf("browser-%d.log", width)))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-extensions", "--remote-debugging-port=0", "--user-data-dir="+driver.profile, fmt.Sprintf("--window-size=%d,900", width), server.URL+"/smoke/login")
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			select {
			case report := <-reports:
				data, _ := json.MarshalIndent(report, "", "  ")
				os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-%d.json", width)), data, 0644)
				if report["complete"] != true {
					t.Fatalf("%s", data)
				}
			case <-time.After(45 * time.Second):
				t.Fatal("review history browser timed out")
			}
		})
	}
}

const codexReviewHistoryProbe = `(async () => {
const report = {}, errors = [];
window.addEventListener("error", event => errors.push(String(event.error || event.message)));
const wait = async (check, label) => { for (let i=0;i<300;i++) { if(check()) return; await new Promise(resolve=>setTimeout(resolve,25)); } throw Error(label); };
try {
await wait(()=>document.querySelector(".assistant-text")?.textContent.includes("Review complete."), "review not loaded");
if (!(await fetch("/smoke/capture")).ok) throw Error("capture failed");
await wait(()=>innerWidth===window.smokeWidth,"viewport");
const check=()=>{
if(document.querySelectorAll(".turn").length!==1 || document.querySelectorAll(".work-label").length!==1) throw Error("duplicate review or work label");
const label=document.querySelector(".work-label").textContent;
if(!label.includes("Worked for 9m 43s") || !label.includes("Codex · session-model · Extra High")) throw Error("missing review metadata: "+label);
if(document.documentElement.scrollWidth>innerWidth+1) throw Error("review overflow");
};
check();
document.querySelector(".work-label-toggle").click();
await wait(()=>document.querySelector(".tool-activity-item-summary"),"process unavailable");
document.querySelector(".tool-activity-item-summary").click();
await wait(()=>[...document.querySelectorAll(".tool-detail")].some(el=>el.textContent.includes("diff output")),"internal tool detail unavailable");
check();
document.querySelector(".work-label-toggle").click();
if(!(await fetch("/smoke/capture")).ok) throw Error("capture failed");
if(errors.length) throw Error(errors.join("; "));
Object.assign(report,{complete:true,oneResponse:true,oneWorkLabel:true,modelAndEffort:true,internalToolDetail:true,noOverflow:true,noBrowserErrors:true});
} catch(error) {report.error=String(error);report.body=document.body.innerText.slice(-2000);}
await fetch("/smoke/report",{method:"POST",body:JSON.stringify(report)});
})();`
