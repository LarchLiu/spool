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
	pinned := true
	if _, err := s.ChangeSidebarPreferences(u.ID, SidebarPreferenceChange{ChatID: target.ID, Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}
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
	var viewedChat atomic.Value
	viewedChat.Store("")
	nativeEvents := (&Service{Store: s}).SubscribeDesktopNotifications()
	t.Cleanup(nativeEvents.Close)
	a.pushClient = &http.Client{Transport: pushTransport(func(*http.Request) (*http.Response, error) {
		delivered.Add(1)
		return pushResponse(201), nil
	})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__desktop/session-changed" || r.URL.Path == "/__desktop/view" {
			// The native navigation guard consumes this hint without replacing
			// the document. A 204 models that behavior in an ordinary browser.
			if r.URL.Path == "/__desktop/view" {
				chat := r.URL.Query().Get("chat")
				viewedChat.Store(chat)
				nativeEvents.SetView(true, chat)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
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
	// Web indicators use authenticated realtime events even without a push
	// subscription. Exercise the actual browser focus and selected-session hints.
	if _, err := s.db.Exec(`DELETE FROM web_push_subscriptions`); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.run(ctx, []browserCommand{
		{Method: "Emulation.setDeviceMetricsOverride", Params: map[string]any{"width": 1440, "height": 900, "deviceScaleFactor": 1, "mobile": false}},
		{Method: "Page.bringToFront"},
		{Method: "Page.navigate", Params: map[string]any{"url": server.URL + "/?chat=" + target.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	waitNotificationView(t, a, cookie.Value, target.ID, true)
	webProbe, err := os.ReadFile("testdata/web-session-notifications-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	checkWebStatus := func(current, other, kind string) {
		t.Helper()
		result, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{
			"expression": fmt.Sprintf("(%s)(%q, %q, %q)", webProbe, current, other, kind), "awaitPromise": true, "returnByValue": true,
		}}})
		var report struct {
			Result           struct{ Value map[string]any }
			ExceptionDetails json.RawMessage
		}
		if err != nil || json.Unmarshal(result, &report) != nil || len(report.ExceptionDetails) > 0 || report.Result.Value["ok"] != true || report.Result.Value["desktop"] != false {
			t.Fatalf("Web session status failed: %s (%v)", result, err)
		}
	}
	for _, kind := range []string{"completed", "failed", "approval"} {
		job := pushJob{chatID: first.ID, turnID: "web-browser-turn", kind: kind}
		if kind == "approval" {
			job.approvalID = "web-browser-approval"
			a.liveMu.Lock()
			a.approvals[job.approvalID] = &pendingApproval{chatID: first.ID, turnID: job.turnID}
			a.liveMu.Unlock()
		}
		a.enqueuePush(pushJob{chatID: target.ID, turnID: job.turnID, kind: kind})
		a.enqueuePush(job)
		checkWebStatus("Notification target", "Other chat", kind)
	}
	if _, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": `
		[...document.querySelectorAll('.nav-chat')].find(row => row.querySelector('.nav-chat-title')?.textContent === 'Other chat').querySelector('.nav-chat-open').click();
	`}}}); err != nil {
		t.Fatal(err)
	}
	waitNotificationView(t, a, cookie.Value, first.ID, true)
	a.enqueuePush(pushJob{chatID: first.ID, kind: "completed"})
	a.enqueuePush(pushJob{chatID: target.ID, kind: "failed"})
	checkWebStatus("Other chat", "Notification target", "failed")
	// Native notification clicks use a preload event rather than Service Worker
	// messaging. Keep authentication inside this disposable browser profile.
	nativeSetup := fmt.Sprintf(`window.spoolDesktop = true; window.spoolNotificationTarget = {url: %q, user: %q};`, "/?inbox="+group.ID+"&recipient="+u.ID, u.ID)
	if _, err := driver.run(ctx, []browserCommand{
		{Method: "Page.enable"},
		{Method: "Emulation.setDeviceMetricsOverride", Params: map[string]any{"width": 1440, "height": 900, "deviceScaleFactor": 1, "mobile": false}},
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
	result, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": fmt.Sprintf("(%s)(%q, %q, %q, %q, %q)", nativeProbe, u.ID, peer.ID, group.ID, target.ID, first.ID), "awaitPromise": true, "returnByValue": true}}})
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
	if viewedChat.Load() != target.ID {
		t.Fatal("desktop navigation did not report the currently viewed session")
	}
	// The following foreground checks exercise the native subscription alone;
	// session indicators also work without any browser push subscription.
	if _, err := s.db.Exec(`DELETE FROM web_push_subscriptions`); err != nil {
		t.Fatal(err)
	}
	for len(nativeEvents.Events()) > 0 {
		nextDesktopEvent(t, nativeEvents)
	}
	for _, kind := range []string{"completed", "failed", "approval"} {
		job := pushJob{chatID: first.ID, turnID: "foreground-browser-turn", kind: kind}
		if kind == "approval" {
			job.approvalID = "foreground-browser-approval"
			a.liveMu.Lock()
			a.approvals[job.approvalID] = &pendingApproval{chatID: first.ID, turnID: job.turnID}
			a.liveMu.Unlock()
		}
		a.enqueuePush(pushJob{chatID: target.ID, turnID: job.turnID, kind: kind})
		a.enqueuePush(job)
		note, allowed := nextDesktopEvent(t, nativeEvents).Resolve(cookie.Value, "en")
		if !allowed || note.ChatID != first.ID || note.Kind != kind || len(nativeEvents.Events()) != 0 {
			t.Fatalf("foreground session notification routed incorrectly: %+v, allowed=%v", note, allowed)
		}
		payload, _ := json.Marshal([]map[string]string{{"chat": note.ChatID, "user": note.UserID, "kind": note.Kind}})
		result, err = driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": fmt.Sprintf(`(async () => {
			window.spoolSessionNotifications = %s;
			window.dispatchEvent(new Event('spool:session-notification-changed'));
			await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
			const rows = [...document.querySelectorAll('.nav-chat')];
			return rows.filter(row => row.querySelector('.nav-chat-title')?.textContent === 'Other chat').every(row => row.querySelector('.session-notification-icon')?.dataset.status === %q)
				&& rows.filter(row => row.querySelector('.nav-chat-title')?.textContent === 'Notification target').every(row => !row.querySelector('.session-notification-icon'));
		})()`, payload, kind), "awaitPromise": true, "returnByValue": true}}})
		var foregroundCheck struct{ Result struct{ Value bool } }
		if err != nil || json.Unmarshal(result, &foregroundCheck) != nil || !foregroundCheck.Result.Value {
			t.Fatalf("foreground %s did not update only the other session: %s (%v)", kind, result, err)
		}
	}
	_, err = driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": fmt.Sprintf(`(() => {
		window.spoolSessionNotifications = [{chat: %q, user: %q, kind: 'failed'}];
		window.dispatchEvent(new Event('spool:session-notification-changed'));
	})()`, target.ID, u.ID)}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err = driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": `new Promise(resolve => requestAnimationFrame(() => {
		const marker = document.querySelector('.pinned .session-notification-icon');
		if (!marker) { resolve(null); return; }
		const row = marker.closest('.nav-chat');
		const rect = row.getBoundingClientRect();
		window.sessionStatusBeforeHover = marker.getBoundingClientRect().x;
		resolve({x: rect.x + rect.width / 2, y: rect.y + rect.height / 2});
	}))`, "awaitPromise": true, "returnByValue": true}}})
	var hover struct {
		Result struct{ Value struct{ X, Y float64 } }
	}
	if err != nil || json.Unmarshal(result, &hover) != nil || hover.Result.Value.X == 0 {
		t.Fatalf("missing session marker hover target: %s (%v)", result, err)
	}
	result, err = driver.run(ctx, []browserCommand{
		{Method: "Input.dispatchMouseEvent", Params: map[string]any{"type": "mouseMoved", "x": hover.Result.Value.X, "y": hover.Result.Value.Y}},
		{Method: "Runtime.evaluate", Params: map[string]any{"expression": `new Promise(resolve => requestAnimationFrame(() => {
			const marker = document.querySelector('.pinned .session-notification-icon');
			const row = marker.closest('.nav-chat'), status = marker.getBoundingClientRect();
			const actions = [...row.querySelectorAll('.nav-chat-actions button')];
			resolve(actions.length === 2 && actions.every(button => {
				const rect = button.getBoundingClientRect();
				return rect.width > 0 && rect.right <= status.left && document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)?.closest('button') === button;
			}) && status.x === window.sessionStatusBeforeHover && row.scrollWidth <= row.clientWidth);
		}))`, "awaitPromise": true, "returnByValue": true}},
	})
	var hoverCheck struct{ Result struct{ Value bool } }
	if err != nil || json.Unmarshal(result, &hoverCheck) != nil || !hoverCheck.Result.Value {
		t.Fatalf("hover actions conflicted with the session marker: %s (%v)", result, err)
	}
	for _, theme := range []string{"light", "dark"} {
		_, err := driver.run(ctx, []browserCommand{{Method: "Runtime.evaluate", Params: map[string]any{"expression": fmt.Sprintf(`(async () => {
			document.documentElement.dataset.theme = %q;
			window.spoolSessionNotifications = [{chat: %q, user: %q, kind: 'completed'}, {chat: %q, user: %q, kind: 'approval'}];
			window.dispatchEvent(new Event('spool:session-notification-changed'));
			await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
		})()`, theme, first.ID, u.ID, target.ID, u.ID), "awaitPromise": true}}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := driver.run(ctx, []browserCommand{{Method: "Page.captureScreenshot", Params: map[string]any{"format": "png"}}})
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
		if err := os.WriteFile(filepath.Join(output, "session-status-"+theme+".png"), png, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if delivered.Load() != 1 {
		t.Fatalf("browser test delivered %d test pushes, want 1", delivered.Load())
	}
}
