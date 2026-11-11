package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run after building frontend/, with CHROME_BIN pointing to a standalone
// Chromium binary. The relay, both homes and browser profile are disposable.
func TestRelayWebBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN to check two relay Web/PWA origins in a real browser")
	}
	_, public, nodes := relayWebFixture(t, 2)
	// An untrusted operator can replace its own HTML and CSP. Do not let the
	// normal Spool CSP mask a missing browser-origin boundary in this test.
	originalAssets := nodes[0].app.assets
	nodes[0].app.assets = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/isolation.html" {
			w.Header().Del("Content-Security-Policy")
			w.Header().Set("Content-Type", "text/html")
			// A malicious tenant cannot use this domain-wide cookie clearing
			// header to log out other origins; the gateway strips it.
			w.Header().Set("Clear-Site-Data", `"cookies"`)
			fmt.Fprint(w, "<!doctype html><title>Untrusted server operator</title>")
			return
		}
		originalAssets.ServeHTTP(w, r)
	})
	output := filepath.Join("..", "..", "build", "artifacts", "relay-web")
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(output, "browser.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	profile := t.TempDir()
	command := exec.Command(chrome, "--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--ignore-certificate-errors", "--no-proxy-server", "--host-resolver-rules=MAP *.relay.localhost 127.0.0.1,MAP relay.localhost 127.0.0.1", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	command.Stdout, command.Stderr = log, log
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	first := &browserDriver{profile: profile}
	second := &browserDriver{profile: profile}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = first.run(ctx, []browserCommand{{Method: "Browser.close"}})
		cancel()
		first.close()
		second.close()
		done := make(chan struct{})
		go func() { command.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			command.Process.Kill()
			<-done
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Chrome did not start; inspect", log.Name())
		case <-time.After(25 * time.Millisecond):
		}
	}
	probe, err := os.ReadFile("testdata/relay-web-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	setup := `window.relayProbe = { sockets: [], streams: [], ready: false };
window.WebSocket = class extends WebSocket {
  constructor(url, ...options) {
    super(url, ...options); window.relayProbe.sockets.push(String(url));
    this.addEventListener('message', event => { try { if (JSON.parse(event.data).type === 'ready') window.relayProbe.ready = true; } catch {} });
  }
};
window.EventSource = class extends EventSource {
  constructor(url, ...options) { super(url, ...options); window.relayProbe.streams.push(String(url)); }
};`
	run := func(driver *browserDriver, commands ...browserCommand) json.RawMessage {
		t.Helper()
		result, err := driver.run(ctx, commands)
		if err != nil {
			driver.close()
			driver.connection = nil
			diagnostic, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			state, _ := driver.run(diagnostic, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": "JSON.stringify({url:location.href,ready:document.readyState,step:window.relayStep,probe:window.relayProbe,text:document.body?.innerText.slice(0,1500),resources:performance.getEntriesByType('resource').map(e=>({path:new URL(e.name).pathname,duration:e.duration}))})", "returnByValue": true}}})
			t.Fatalf("browser %s: %v; state: %s", commands[len(commands)-1].Method, err, state)
		}
		return result
	}
	evaluate := func(driver *browserDriver, expression string) json.RawMessage {
		t.Helper()
		result := run(driver, browserCommand{Method: "Runtime.evaluate", Params: map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true}})
		var value struct {
			Result           struct{ Value json.RawMessage }
			ExceptionDetails json.RawMessage
		}
		if err := json.Unmarshal(result, &value); err != nil || len(value.ExceptionDetails) > 0 {
			t.Fatalf("browser evaluation: %s (%v)", result, err)
		}
		return value.Result.Value
	}
	for i, driver := range []*browserDriver{first, second} {
		t.Logf("opening relay server %d", i+1)
		if i == 1 {
			var created struct{ TargetID string }
			if err := json.Unmarshal(run(first, browserCommand{Method: "Target.createTarget", Params: map[string]any{"url": "about:blank"}}), &created); err != nil || created.TargetID == "" {
				t.Fatal("could not create a second tab", err)
			}
			second.targetID = created.TargetID
		}
		run(driver,
			browserCommand{Method: "Page.enable"},
			browserCommand{Method: "Page.addScriptToEvaluateOnNewDocument", Params: map[string]any{"source": setup}},
			browserCommand{Method: "Page.navigate", Params: map[string]any{"url": nodes[i].url}},
		)
		// Page.navigate returns before the document's execution context changes.
		for {
			state := evaluate(driver, fmt.Sprintf("location.href === %q && document.readyState === 'complete'", nodes[i].url))
			if string(state) == "true" {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("relay page did not load")
			case <-time.After(25 * time.Millisecond):
			}
		}
		t.Logf("checking login and PWA on server %d", i+1)
		if string(evaluate(driver, "!!window.relayProbe")) != "true" {
			t.Fatal("browser probe was not installed before navigation")
		}
		result := evaluate(driver, fmt.Sprintf("(%s)(%q)", probe, nodes[i].user.ID))
		// The app identity is shared by this origin. Unlike scope/start_url,
		// an explicit relative id resolves from the origin, not the manifest URL.
		var processed struct {
			Manifest struct{ ID, Scope, StartURL string }
		}
		if err := json.Unmarshal(run(driver, browserCommand{Method: "Page.getAppManifest"}), &processed); err != nil {
			t.Fatal("could not read browser manifest", err)
		}
		if processed.Manifest.ID != nodes[i].url || processed.Manifest.Scope != nodes[i].url || processed.Manifest.StartURL != nodes[i].url {
			t.Fatalf("unexpected isolated PWA identity or scope for server %d: %+v", i+1, processed.Manifest)
		}
		if err := os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-server-%d.json", i+1)), result, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// An operator controls arbitrary scripts on its own server. Exercise the
	// sibling-domain boundary from that actual browser execution context, with
	// the victim logged in. Include credentials and even a known victim CSRF to
	// prove Origin enforcement independently of the inability to read that CSRF.
	evaluate(second, `localStorage.setItem('tenant-secret','victim-only')`)
	victimCSRF := evaluate(second, `(async()=> (await (await fetch('/api/me')).json()).csrf)()`)
	run(first, browserCommand{Method: "Page.navigate", Params: map[string]any{"url": nodes[0].url + "isolation.html"}})
	for string(evaluate(first, `document.title === 'Untrusted server operator' && document.readyState === 'complete'`)) != "true" {
		select {
		case <-ctx.Done():
			t.Fatal("attacker document did not load")
		case <-time.After(25 * time.Millisecond):
		}
	}
	attack := fmt.Sprintf(`(async()=>{
 const victim=%q;
 const blocked=async work=>{try{await work();return false}catch{return true}};
 const readBlocked=await blocked(()=>fetch(victim+'api/me',{credentials:'include'}).then(r=>r.json()));
 const writeBlocked=await blocked(()=>fetch(victim+'api/logout',{method:'POST',credentials:'include',headers:{'Content-Type':'application/json','X-CSRF-Token':%s},body:'{}'}));
 await fetch(victim+'api/logout',{method:'POST',mode:'no-cors',credentials:'include',headers:{'Content-Type':'text/plain'},body:'{}'});
 const workerBlocked=await blocked(()=>navigator.serviceWorker.register(victim+'sw.js',{scope:victim}));
 const socketBlocked=await new Promise(resolve=>{const socket=new WebSocket(victim.replace('https:','wss:')+'api/events');socket.onopen=()=>{socket.close();resolve(false)};socket.onerror=()=>resolve(true);setTimeout(()=>{socket.close();resolve(true)},3000)});
 document.cookie='__Host-spool_session=attacker; Domain=relay.localhost; Path=/; Secure';
 document.cookie='spool_session=attacker; Domain=relay.localhost; Path=/; Secure';
 return {readBlocked,writeBlocked,workerBlocked,socketBlocked,storageIsolated:localStorage.getItem('tenant-secret')===null,hostCookieProtected:!document.cookie.includes('__Host-spool_session')};
})()`, nodes[1].url, victimCSRF)
	observed := evaluate(first, attack)
	var checks map[string]bool
	if err := json.Unmarshal(observed, &checks); err != nil || len(checks) != 6 {
		t.Fatal("invalid isolation result", string(observed), err)
	}
	for check, passed := range checks {
		if !passed {
			t.Fatal("cross-tenant isolation failed", check, string(observed))
		}
	}
	if string(evaluate(second, `fetch('/api/me').then(r=>r.status)`)) != "200" {
		t.Fatal("sibling script altered victim authentication")
	}
	if err := os.WriteFile(filepath.Join(output, "isolation.json"), observed, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("CS-01 browser isolation: %s", observed)
	// Both tabs stay signed in to their own database on separate origins.
	t.Log("checking both sessions")
	for i, driver := range []*browserDriver{first, second} {
		value := evaluate(driver, `(async () => (await (await fetch('./api/me')).json()).user.id)()`)
		if string(value) != fmt.Sprintf("%q", nodes[i].user.ID) {
			t.Fatal("another tab replaced the session", string(value))
		}
	}
	// Legacy cleanup must not clear cookies across the parent domain. Visiting
	// the old shared mount in a third tab must leave both new origins signed in.
	var legacyTab struct{ TargetID string }
	if err := json.Unmarshal(run(first, browserCommand{Method: "Target.createTarget", Params: map[string]any{"url": "about:blank"}}), &legacyTab); err != nil || legacyTab.TargetID == "" {
		t.Fatal("could not create legacy migration tab", err)
	}
	legacy := &browserDriver{profile: profile, targetID: legacyTab.TargetID}
	defer legacy.close()
	run(legacy, browserCommand{Method: "Page.enable"}, browserCommand{Method: "Page.navigate", Params: map[string]any{"url": public.URL + "/web/" + nodes[0].id.ID() + "/"}})
	for string(evaluate(legacy, `document.readyState === 'complete' && document.body?.innerText.includes('Relay Web has moved')`)) != "true" {
		select {
		case <-ctx.Done():
			t.Fatal("legacy migration response did not load")
		case <-time.After(25 * time.Millisecond):
		}
	}
	for _, driver := range []*browserDriver{first, second} {
		if string(evaluate(driver, `fetch('/api/me').then(r=>r.status)`)) != "200" {
			t.Fatal("legacy migration cleared another origin's session")
		}
	}
	run(first, browserCommand{Method: "Target.closeTarget", Params: map[string]any{"targetId": legacyTab.TargetID}})
	// A new navigation restores the authenticated page and its origin's SW.
	t.Log("checking reload and offline fallback")
	run(first, browserCommand{Method: "Page.navigate", Params: map[string]any{"url": nodes[0].url}})
	for {
		value := evaluate(first, "document.readyState === 'complete' && window.relayProbe?.ready && !!navigator.serviceWorker.controller")
		if string(value) == "true" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("relay page did not recover after reload")
		case <-time.After(50 * time.Millisecond):
		}
	}
	nodes[0].host.Close()
	run(first, browserCommand{Method: "Page.reload"})
	for {
		if string(evaluate(first, "document.readyState === 'complete' && !!document.querySelector('#offline-title')")) == "true" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("relay outage did not show the offline page")
		case <-time.After(50 * time.Millisecond):
		}
	}
	value := evaluate(second, "fetch('./api/me').then(response => response.status)")
	if string(value) != "200" {
		t.Fatal("stopping one server affected the other", string(value))
	}
}
