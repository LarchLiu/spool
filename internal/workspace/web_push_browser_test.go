package workspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

// The browser uses a fake PushManager endpoint; crypto and delivery are covered
// separately, so this smoke never contacts a real user's push service.
func TestPushBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("set CHROME_BIN to check Web Push controls in a real browser")
	}
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	first, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	target, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	s.db.Exec(`UPDATE sessions SET title=CASE id WHEN ? THEN 'Notification target' ELSE 'Other chat' END`, target.ID)
	if err := s.SaveUIPreferences(u.ID, UIPreferences{Theme: "light", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	peer := testUser(t, s, "bob")
	_, peerIdentity, err := s.login(peer.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	a.presence.Touch(peerIdentity, "push-browser-peer", true)
	peerClient := a.directMessages.subscribe(peer.ID)
	t.Cleanup(func() { a.directMessages.unsubscribe(peerClient) })
	group, code := a.changePeopleChatGroup(&directMessageClient{user: u.ID}, realtimeInput{Type: "group_create", RequestID: "push-browser-group", Name: "Push group", Members: []string{peer.ID}})
	if code != "" {
		t.Fatal(code)
	}
	cookie, _ := loginRequest(t, a, u.Username)
	sub, _ := testPushSubscription(t, "browser-smoke")
	var delivered atomic.Int64
	a.pushClient = &http.Client{Transport: pushTransport(func(*http.Request) (*http.Response, error) {
		delivered.Add(1)
		return pushResponse(201), nil
	})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/smoke/login" {
			http.SetCookie(w, cookie)
			http.Redirect(w, r, "/?chat="+target.ID, http.StatusFound)
			return
		}
		a.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { a.Close(); server.Close() })
	output := filepath.Join("..", "..", "build", "artifacts", "web-push")
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(output, "browser.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	profile := t.TempDir()
	command := exec.Command(chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	driver := &browserDriver{profile: profile}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = driver.run(ctx, []browserCommand{{Method: "Browser.close"}})
		cancel()
		driver.close()
		done := make(chan struct{})
		go func() { command.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			command.Process.Kill()
			<-done
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	body, _ := json.Marshal(sub)
	setup := fmt.Sprintf(`window.pushSmokeSubscription = null;
PushManager.prototype.getSubscription = async function() { return window.pushSmokeSubscription; };
PushManager.prototype.subscribe = async function(options) {
  const value = %s;
  window.pushSmokeSubscription = {
    endpoint: value.endpoint, options: { applicationServerKey: options.applicationServerKey.buffer },
    toJSON() { return value; },
    async unsubscribe() { window.pushSmokeSubscription = null; return true; }
  };
  return window.pushSmokeSubscription;
};`, body)
	if _, err := driver.run(ctx, []browserCommand{
		{Method: "Browser.grantPermissions", Params: map[string]any{"origin": server.URL, "permissions": []string{"notifications"}}},
		{Method: "Page.addScriptToEvaluateOnNewDocument", Params: map[string]any{"source": setup}},
		{Method: "Page.navigate", Params: map[string]any{"url": server.URL + "/smoke/login"}},
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := os.ReadFile("testdata/web-push-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{1440, 390} {
		if width == 390 {
			if _, err := driver.run(ctx, []browserCommand{{Method: "Page.navigate", Params: map[string]any{"url": server.URL + "/?inbox=" + group.ID + "&recipient=" + u.ID}}}); err != nil {
				t.Fatal(err)
			}
			for {
				result, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": "document.readyState === 'complete' && !!document.querySelector('.direct-message-panel')", "returnByValue": true}}})
				var state struct{ Result struct{ Value bool } }
				if err == nil && json.Unmarshal(result, &state) == nil && state.Result.Value {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("fresh inbox page did not open its conversation")
				case <-time.After(25 * time.Millisecond):
				}
			}
		}
		_, err := driver.run(ctx, []browserCommand{{Method: "Emulation.setDeviceMetricsOverride", Params: map[string]any{"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": width < 900}}})
		if err != nil {
			t.Fatal(err)
		}
		expression := fmt.Sprintf("(%s)(%q, %d, %q, %q, %q)", probe, first.ID, width, u.ID, peer.ID, group.ID)
		result, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true}}})
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Result           struct{ Value map[string]any }
			ExceptionDetails json.RawMessage
		}
		if err := json.Unmarshal(result, &report); err != nil || len(report.ExceptionDetails) > 0 || report.Result.Value["ok"] != true {
			t.Fatalf("browser smoke %d failed: %s (%v)", width, result, err)
		}
		if err := os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-%d.json", width)), result, 0644); err != nil {
			t.Fatal(err)
		}
		result, err = driver.run(ctx, []browserCommand{{Method: "Page.captureScreenshot", Params: map[string]any{"format": "png"}}})
		if err != nil {
			t.Fatal(err)
		}
		var image struct{ Data string }
		if err := json.Unmarshal(result, &image); err != nil {
			t.Fatal(err)
		}
		png, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, fmt.Sprintf("browser-%d.png", width)), png, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Native notification clicks use a preload event rather than Service Worker
	// messaging. Keep authentication inside this disposable browser profile.
	nativeSetup := fmt.Sprintf(`window.spoolDesktop = true; window.spoolNotificationTarget = {url: %q, user: %q};`, "/?inbox="+group.ID+"&recipient="+u.ID, u.ID)
	if _, err := driver.run(ctx, []browserCommand{
		{Method: "Page.enable"},
		{Method: "Page.addScriptToEvaluateOnNewDocument", Params: map[string]any{"source": nativeSetup}},
		{Method: "Page.navigate", Params: map[string]any{"url": server.URL + "/"}},
	}); err != nil {
		t.Fatal(err)
	}
	nativeDeadline := time.Now().Add(5 * time.Second)
	for {
		result, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": "window.spoolDesktop === true && !!document.querySelector('.direct-message-panel')", "returnByValue": true}}})
		var state struct{ Result struct{ Value bool } }
		if err == nil && json.Unmarshal(result, &state) == nil && state.Result.Value {
			break
		}
		if time.Now().After(nativeDeadline) {
			detail, _ := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": "JSON.stringify({ready:document.readyState,native:window.spoolDesktop,target:window.spoolNotificationTarget,text:document.body?.innerText.slice(0,2500)})", "returnByValue": true}}})
			t.Fatalf("native notification page did not become ready: %s", detail)
		}
		select {
		case <-ctx.Done():
			t.Fatal("native notification page did not become ready")
		case <-time.After(25 * time.Millisecond):
		}
	}
	nativeProbe, err := os.ReadFile("testdata/desktop-notifications-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	result, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": fmt.Sprintf("(%s)(%q, %q, %q, %q)", nativeProbe, u.ID, peer.ID, group.ID, target.ID), "awaitPromise": true, "returnByValue": true}}})
	if err != nil {
		t.Fatal(err)
	}
	var nativeReport struct {
		Result           struct{ Value map[string]any }
		ExceptionDetails json.RawMessage
	}
	if err := json.Unmarshal(result, &nativeReport); err != nil || len(nativeReport.ExceptionDetails) > 0 || nativeReport.Result.Value["ok"] != true {
		t.Fatalf("native notification browser routing failed: %s (%v)", result, err)
	}
	if delivered.Load() != 1 {
		t.Fatalf("browser test delivered %d test pushes, want 1", delivered.Load())
	}
}
