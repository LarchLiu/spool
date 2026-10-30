package workspace

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	chatmodel "spool/internal/model"
)

func TestAdditionalTLSListenerSharesAuthenticationStreamingAndShutdown(t *testing.T) {
	service, settings := settingsService(t)
	user := testUser(t, service.Store, "tailnet")
	project := testProject(t, service.Store, user)
	chat, err := service.Store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	config := seed.TLS.Clone()
	client := seed.Client()
	client.Timeout = 5 * time.Second
	seed.Close()
	t.Cleanup(client.CloseIdleConnections)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	tailnetOrigin := "https://" + listener.Addr().String()
	publicOrigin := "https://chat.example.test"
	settings.PublicURL = publicOrigin
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	app := service.app
	if err := service.ServeListener(listener); err != nil {
		t.Fatal(err)
	}
	if service.app != app || service.WebURL() != publicOrigin {
		t.Fatal("additional ingress replaced the app or public PWA origin")
	}
	if err := service.ServeListener(nil); err == nil {
		t.Fatal("accepted a nil listener")
	}
	request := func(base, method, path, body, requestOrigin string, cookie *http.Cookie, csrf string, want int) (*http.Response, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", requestOrigin)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d: %s (%v)", method, path, response.StatusCode, want, data, err)
		}
		return response, data
	}
	request(tailnetOrigin, "GET", "/healthz", "", "", nil, "", 200)
	request(tailnetOrigin, "GET", "/api/me", "", "", nil, "", 401)
	credentials := `{"username":"tailnet","password":"test-password-2026"}`
	request(tailnetOrigin, "POST", "/api/login", credentials, "https://evil.example", nil, "", 403)
	response, body := request(tailnetOrigin, "POST", "/api/login", credentials, tailnetOrigin, nil, "", 200)
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Domain != "" {
		t.Fatal("TLS ingress lost secure authentication cookie properties", cookies)
	}
	cookie := cookies[0]
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(body, &login); err != nil || login.CSRF == "" {
		t.Fatal("missing login CSRF token", string(body), err)
	}
	local, err := service.LocalWebURL()
	if err != nil {
		t.Fatal(err)
	}
	request(local, "GET", "/api/me", "", local, cookie, "", 200)
	request(tailnetOrigin, "POST", "/api/preferences", `{"theme":"dark","language":"en"}`, tailnetOrigin, cookie, "", 403)
	request(tailnetOrigin, "POST", "/api/preferences", `{"theme":"dark","language":"en"}`, "https://evil.example", cookie, login.CSRF, 403)
	request(tailnetOrigin, "POST", "/api/preferences", `{"theme":"dark","language":"en"}`, tailnetOrigin, cookie, login.CSRF, 200)
	proxyResponse, proxyBody := request(local, "POST", "/api/login", credentials, publicOrigin, nil, "", 200)
	proxyCookies := proxyResponse.Cookies()
	if len(proxyCookies) != 1 || !proxyCookies[0].Secure || !proxyCookies[0].HttpOnly || proxyCookies[0].SameSite != http.SameSiteStrictMode || proxyCookies[0].Domain != "" {
		t.Fatal("reverse-proxy origin lost secure authentication cookie properties", proxyCookies)
	}
	var proxyLogin struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(proxyBody, &proxyLogin); err != nil || proxyLogin.CSRF == "" {
		t.Fatal("reverse-proxy origin missing login CSRF token", string(proxyBody), err)
	}
	request(local, "POST", "/api/login", credentials, publicOrigin+".evil.example", nil, "", 403)
	request(local, "POST", "/api/preferences", `{"theme":"light","language":"en"}`, publicOrigin, proxyCookies[0], "", 403)
	request(local, "POST", "/api/preferences", `{"theme":"light","language":"en"}`, "https://evil.example", proxyCookies[0], proxyLogin.CSRF, 403)
	request(local, "POST", "/api/preferences", `{"theme":"light","language":"en"}`, publicOrigin, proxyCookies[0], proxyLogin.CSRF, 200)
	if proxyCookies[0].Value == cookie.Value {
		t.Fatal("separate sign-ins unexpectedly reused a session cookie")
	}
	preferences, preferencesBody := request(tailnetOrigin, "GET", "/api/preferences", "", tailnetOrigin, cookie, "", 200)
	if !strings.Contains(string(preferencesBody), `"theme":"light"`) {
		t.Fatal("tailnet ingress did not see changes from the PWA entrance", string(preferencesBody))
	}
	if preferences.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("dual ingress enabled cross-origin access")
	}

	sse, err := http.NewRequest("GET", tailnetOrigin+"/api/chats/"+chat.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	sse.AddCookie(cookie)
	stream, err := client.Do(sse)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := bufio.NewReader(stream.Body).ReadString('\n')
	stream.Body.Close()
	if err != nil || frame != "event: snapshot\n" || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("TLS ingress did not flush the SSE snapshot", frame, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, tailnetOrigin+"/api/events", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Origin": {tailnetOrigin}, "Cookie": {cookie.String()}},
	})
	if err != nil {
		t.Fatal("WSS ingress failed", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	realtimeWrite(t, conn, realtimeInput{Type: "hello", CSRF: login.CSRF, Visible: true})
	realtimeReady(t, conn)
	proxyConn, _, err := websocket.Dial(ctx, local+"/api/events", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Origin": {publicOrigin}, "Cookie": {proxyCookies[0].String()}},
	})
	if err != nil {
		t.Fatal("reverse-proxy WebSocket failed while TLS ingress was active", err)
	}
	t.Cleanup(func() { proxyConn.CloseNow() })
	realtimeWrite(t, proxyConn, realtimeInput{Type: "hello", CSRF: proxyLogin.CSRF, Visible: true})
	realtimeReady(t, proxyConn)
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-app.shutdown:
	default:
		t.Fatal("stopping ingress did not close the shared app")
	}
	for _, stream := range []*websocket.Conn{conn, proxyConn} {
		for {
			if _, _, err := stream.Read(ctx); err != nil {
				if ctx.Err() != nil {
					t.Fatal("stopping the service left a WebSocket open", err)
				}
				break
			}
		}
	}
	if response, err := client.Get(tailnetOrigin + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("stopping the service left TLS ingress open")
	}
}

func TestAdditionalListenerRequiresRunningServiceAndRetainsOwnershipOnFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, service := range []*Service{{}, {closed: true, server: &http.Server{}}} {
		if err := service.ServeListener(listener); err == nil {
			t.Fatal("stopped/closed service accepted additional ingress")
		}
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("failed registration closed the caller-owned listener", err)
	}
	conn.Close()
}
