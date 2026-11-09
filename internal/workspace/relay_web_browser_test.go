package workspace

import (
	"context"
	"encoding/json"
	"fmt"
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
		t.Skip("set CHROME_BIN to check two relay Web/PWA mounts in a real browser")
	}
	_, _, nodes := relayWebFixture(t, 2)
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
	command := exec.Command(chrome, "--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
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
		if err := os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-server-%d.json", i+1)), result, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Both tabs stay signed in to their own database on the same relay origin.
	t.Log("checking both sessions")
	for i, driver := range []*browserDriver{first, second} {
		value := evaluate(driver, `(async () => (await (await fetch('./api/me')).json()).user.id)()`)
		if string(value) != fmt.Sprintf("%q", nodes[i].user.ID) {
			t.Fatal("another tab replaced the session", string(value))
		}
	}
	// A new navigation restores the authenticated page and its path-scoped SW.
	t.Log("checking reload and offline fallback")
	run(first, browserCommand{Method: "Page.reload"})
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
