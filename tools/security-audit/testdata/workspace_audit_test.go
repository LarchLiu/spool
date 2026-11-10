package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These probes pass when the audited behavior is reproduced, not when it is fixed.
// All accounts, network services, cookies, and browser state are disposable.
func TestAuditPlaintextRemoteLogin(t *testing.T) {
	for _, public := range []string{"", "https://public.example.test"} {
		t.Run(fmt.Sprintf("public_https_%t", public != ""), func(t *testing.T) {
			store := testStore(t)
			testUser(t, store, "audit")
			settings := DefaultSettings()
			settings.Host, settings.PublicURL = "0.0.0.0", public
			if err := store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { _ = app.Close() })
			r := httptest.NewRequest("POST", "http://192.0.2.10:8787/api/login", strings.NewReader(`{"username":"audit","password":"test-password-2026"}`))
			r.RemoteAddr = "192.0.2.20:45000"
			r.Header.Set("Origin", "http://192.0.2.10:8787")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("plaintext login status=%d", w.Code)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatal("expected synthetic session")
			}
			if cookies[0].Secure != (public != "") {
				t.Fatal("unexpected cookie policy")
			}
			read := httptest.NewRequest("GET", "http://192.0.2.10:8787/api/me", nil)
			read.RemoteAddr = r.RemoteAddr
			read.AddCookie(cookies[0])
			out := httptest.NewRecorder()
			app.ServeHTTP(out, read)
			if out.Code != 200 {
				t.Fatalf("session replay status=%d", out.Code)
			}
			t.Logf("remote plaintext login=200, cookie Secure=%t, HTTP session replay=200", cookies[0].Secure)
		})
	}
}

func TestAuditProxyLoginLimitIsShared(t *testing.T) {
	store := testStore(t)
	testUser(t, store, "audit")
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	for i := 0; i < 20; i++ {
		r := httptest.NewRequest("POST", "http://public.example.test/api/login", nil)
		r.RemoteAddr = fmt.Sprintf("127.0.0.1:%d", 30000+i)
		r.Header.Set("X-Forwarded-For", "192.0.2.1")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 415 {
			t.Fatalf("attempt %d status=%d", i, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://public.example.test/api/login", strings.NewReader(`{"username":"audit","password":"test-password-2026"}`))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "198.51.100.2")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("other client's login status=%d", w.Code)
	}
	t.Log("20 malformed requests from one proxied client block a different client's valid login with 429")
}

func TestAuditRelayCrossTenantBrowser(t *testing.T) {
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		t.Skip("CHROME_BIN required")
	}
	_, _, nodes := relayWebFixture(t, 2)
	victim := nodes[1]
	attack := fmt.Sprintf(`(async()=>{
 const response = await fetch(%q+'api/me', {credentials:'same-origin'});
 const me = await response.json();
 const write = await fetch(%q+'api/logout', {method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':me.csrf},body:'{}'});
 const after = await fetch(%q+'api/me');
 window.audit = {readStatus:response.status,matchedUser:me.user.id===%q,mutationStatus:write.status,afterStatus:after.status,canReadHttpOnlyCookie:document.cookie.includes('spool_session')};
})().catch(e=>{window.audit={error:String(e)}});`, victim.url, victim.url, victim.url, victim.user.ID)
	nodes[0].app.assets = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "attack.js") {
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, attack)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Isolated audit fixture</title><script src="./attack.js"></script>`)
	})
	profile := t.TempDir()
	command := exec.Command(chrome, "--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
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
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("isolated Chrome did not start")
		case <-time.After(25 * time.Millisecond):
		}
	}
	run := func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		result, err := driver.run(ctx, []browserCommand{{Method: method, Params: params}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	eval := func(expression string) json.RawMessage {
		t.Helper()
		var value struct {
			Result           struct{ Value json.RawMessage }
			ExceptionDetails json.RawMessage
		}
		result := run("Runtime.evaluate", map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true})
		if err := json.Unmarshal(result, &value); err != nil || len(value.ExceptionDetails) > 0 {
			t.Fatal("browser evaluation failed")
		}
		return value.Result.Value
	}
	navigate := func(address string) {
		t.Helper()
		run("Page.navigate", map[string]any{"url": address})
		for string(eval(fmt.Sprintf("location.href===%q && document.readyState==='complete'", address))) != "true" {
			select {
			case <-ctx.Done():
				t.Fatal("browser navigation deadline")
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	run("Page.enable", nil)
	navigate(victim.url + "api/version")
	login := eval(`(async()=> (await fetch('./login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:'alice',password:'test-password-2026'})})).status)()`)
	if string(login) != "200" {
		t.Fatalf("fixture login=%s", login)
	}
	navigate(nodes[0].url + "attack.html")
	for string(eval("!!window.audit")) != "true" {
		select {
		case <-ctx.Done():
			t.Fatal("attack fixture deadline")
		case <-time.After(25 * time.Millisecond):
		}
	}
	result := eval("window.audit")
	var observed struct {
		ReadStatus, MutationStatus, AfterStatus int
		MatchedUser, CanReadHttpOnlyCookie      bool
		Error                                   string
	}
	if err := json.Unmarshal(result, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Error != "" || observed.ReadStatus != 200 || !observed.MatchedUser || observed.MutationStatus != 200 || observed.AfterStatus != 401 || observed.CanReadHttpOnlyCookie {
		t.Fatalf("unexpected observation: %s", result)
	}
	t.Logf("cross-tenant script on shared relay origin: %s", result)
}
