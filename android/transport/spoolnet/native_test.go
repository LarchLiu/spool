package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func nativeFixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	origin, _ := url.Parse("https://example.com")
	httpClient, transport := nativeHTTPClient(func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "example.com:443" {
			t.Errorf("unexpected native dial: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	})
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); transport.CloseIdleConnections() })
	return &Client{http: httpClient, transport: transport, origin: origin, ctx: ctx, cancel: cancel}
}

func TestNativeHTTPSCookieCSRFAndOrigin(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.Host != "example.com" || r.Header.Get("Origin") != "https://example.com" || r.Header.Get("X-UI-Language") != "zh-CN" {
			t.Error("lost exact HTTPS origin or language")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "test-session", Path: "/", Secure: true, HttpOnly: true})
		} else {
			cookie, err := r.Cookie("spool_session")
			if err != nil || cookie.Value != "test-session" {
				t.Error("native session cookie missing")
			}
			if r.Method == "POST" && r.Header.Get("X-CSRF-Token") != "test-csrf" {
				t.Error("native mutation lost CSRF")
			}
		}
		io.WriteString(w, `{"ok":true}`)
	})
	for _, path := range []string{"/api/login", "/api/me", "/api/preferences"} {
		raw, err := client.Request("POST", path, `{}`, "test-csrf", "zh-CN")
		if err != nil {
			t.Fatal(err)
		}
		var result apiResponse
		if json.Unmarshal([]byte(raw), &result) != nil || result.Status != 200 || result.Body != `{"ok":true}` {
			t.Fatalf("invalid native response: %s", raw)
		}
	}
	if client.transport.Proxy != nil {
		t.Fatal("native API must not inherit proxies")
	}
}

func TestNativeChatRequestsNegotiateLazyToolBodies(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/chats/") && r.Header.Get("X-Tool-Details") != "lazy" {
			t.Error("native chat request still downloads full tool bodies")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	for _, path := range []string{"/api/chats/chat", "/api/chats/chat/history?before=cursor", "/api/chats/chat/messages", "/api/chats/chat/stop"} {
		method, body := "GET", ""
		if strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/stop") {
			method, body = "POST", `{}`
		}
		if _, err := client.Request(method, path, body, "csrf", "en"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeRejectsRedirectAndForeignPaths(t *testing.T) {
	var requests atomic.Int32
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://attacker.example/api/me", http.StatusFound)
	})
	for _, path := range []string{"https://attacker.example/api/me", "//attacker.example/api/me", "/healthz", "/api/../secret", "/api/%2e%2e/secret", "/api/one\\two", "file:///api/me"} {
		if _, err := client.Request("GET", path, "", "", "en"); err == nil {
			t.Errorf("accepted unsafe path %q", path)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("unsafe paths reached the network")
	}
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err == nil {
		t.Fatal("redirect followed")
	}
	if requests.Load() != 1 {
		t.Fatal("redirect generated an extra request")
	}
	client.transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	client.transport.CloseIdleConnections()
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err == nil {
		t.Fatal("invalid TLS certificate accepted")
	}
}

func TestNativeResponseBoundsAndBinary(t *testing.T) {
	if _, err := encodeResponse(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1)))}); err == nil {
		t.Fatal("oversized response accepted")
	}
	raw, err := encodeResponse(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(strings.NewReader("\x00\xff"))})
	var result apiResponse
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || !result.Base64 || result.Body != "AP8=" {
		t.Fatal("binary response corrupted", raw, err)
	}
}

func TestNativeFilePayloadsKeepEveryByteIncludingLegacyTextEncodings(t *testing.T) {
	for _, media := range []string{"text/plain; charset=iso-8859-1", "application/json", "application/octet-stream"} {
		const data = "caf\xe9\r\n\x00\xff"
		raw, err := encodeResponse(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(strings.NewReader(data))})
		var response apiResponse
		if err != nil || json.Unmarshal([]byte(raw), &response) != nil || !response.Base64 {
			t.Fatalf("%s lost binary encoding: %s, %v", media, raw, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(response.Body)
		if err != nil || string(decoded) != data {
			t.Fatalf("%s corrupted downloaded bytes: %q, %v", media, decoded, err)
		}
	}
}

func TestNativeAttachmentDownloadsStayOnTheAuthenticatedExactHTTPSOrigin(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "file-session", Path: "/", Secure: true, HttpOnly: true})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "file-session" || r.TLS == nil || r.Host != "example.com" || r.Header.Get("Origin") != "https://example.com" ||
			r.Method != http.MethodGet || r.URL.Path != "/api/chats/chat/attachments" || r.URL.Query().Get("id") != "image + one" {
			t.Errorf("file left the authorized native channel: %v, %v", r.URL, err)
		}
		w.Header().Set("Content-Type", "text/plain; charset=iso-8859-1")
		io.WriteString(w, "caf\xe9\r\n")
	})
	if _, err := client.Request("POST", "/api/login", `{}`, "", "en"); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Request("GET", "/api/chats/chat/attachments?id="+url.QueryEscape("image + one"), "", "", "")
	var response apiResponse
	if err != nil || json.Unmarshal([]byte(raw), &response) != nil || response.Status != 200 || !response.Base64 {
		t.Fatalf("invalid download response: %s, %v", raw, err)
	}
	bytes, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil || string(bytes) != "caf\xe9\r\n" {
		t.Fatalf("attachment bytes changed: %q, %v", bytes, err)
	}
}

func TestNativeAvatarStaysOnAuthenticatedHTTPSOrigin(t *testing.T) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0h96v96H0z"/></svg>`
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "avatar-session", Path: "/", Secure: true, HttpOnly: true})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "avatar-session" || r.TLS == nil || r.Host != "example.com" ||
			r.Method != "GET" || r.URL.Path != "/api/avatars.svg" || r.URL.Query().Get("seed") != "Alice + 中文" {
			t.Errorf("avatar escaped authenticated exact-origin channel: %v, %v", r.URL, err)
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		io.WriteString(w, svg)
	})
	if _, err := client.Request("POST", "/api/login", `{}`, "", "en"); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Request("GET", "/api/avatars.svg?style=clay&seed="+url.QueryEscape("Alice + 中文"), "", "", "")
	var result apiResponse
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || result.Status != 200 ||
		result.MediaType != "image/svg+xml; charset=utf-8" || !result.Base64 {
		t.Fatalf("invalid native SVG response: %s, %v", raw, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Body)
	if err != nil || string(decoded) != svg {
		t.Fatalf("SVG was changed by the native channel: %q, %v", decoded, err)
	}
}

type eventRecorder chan [2]string

func (r eventRecorder) OnEvent(kind, data string) { r <- [2]string{kind, data} }

func TestNativeSSEParser(t *testing.T) {
	events := make(eventRecorder, 4)
	err := readSSE(strings.NewReader(": heartbeat\n\nevent: snapshot\ndata: {\ndata: \"id\":\"chat\"}\n\nevent: turn\ndata: {\"status\":\"running\"}\n\n"), events)
	if err != nil {
		t.Fatal(err)
	}
	if event := <-events; event != [2]string{"snapshot", "{\n\"id\":\"chat\"}"} {
		t.Fatal(event)
	}
	if event := <-events; event[0] != "turn" {
		t.Fatal(event)
	}
	if err := readSSE(strings.NewReader("data: "+strings.Repeat("x", maxResponseBytes+1)), events); err == nil {
		t.Fatal("unbounded SSE event")
	}
}

func TestNativeSSECancellation(t *testing.T) {
	closed := make(chan struct{})
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: snapshot\ndata: {\"id\":\"chat\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	})
	events := make(eventRecorder, 4)
	stream, err := client.SubscribeChat("chat", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event[0] != "snapshot" {
			t.Fatal(event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no snapshot")
	}
	stream.Close()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE did not release request on close")
	}
	select {
	case event := <-events:
		t.Fatal("closed stream published", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestNativeWorkspaceWebSocket(t *testing.T) {
	sent := make(chan string, 1)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://example.com" {
			t.Error("missing websocket origin")
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		_, hello, err := conn.Read(ctx)
		if err != nil || !strings.Contains(string(hello), `"csrf":"csrf-token"`) {
			t.Error("missing CSRF hello")
			return
		}
		conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ready","data":true}`))
		conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping","data":true}`))
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if strings.Contains(string(data), `"type":"direct_message"`) {
				sent <- string(data)
				return
			}
		}
	})
	events := make(eventRecorder, 4)
	stream, err := client.SubscribeWorkspace("csrf-token", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-events:
		if event[0] != "ready" {
			t.Fatal(event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no websocket snapshot")
	}
	if err := stream.Send(`{"type":"direct_message","to":"person","text":"hello"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("native message not sent")
	}
}

func TestNativeWorkspaceReturnsCancelableHandleBeforeHandshakeCompletes(t *testing.T) {
	entered, released := make(chan struct{}), make(chan struct{})
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(released)
	})
	events := make(eventRecorder, 4)
	type result struct {
		stream *EventStream
		err    error
	}
	created := make(chan result, 1)
	go func() { stream, err := client.SubscribeWorkspace("csrf", "en", events); created <- result{stream, err} }()
	var stream *EventStream
	select {
	case value := <-created:
		if value.err != nil {
			t.Fatal(value.err)
		}
		stream = value.stream
	case <-time.After(3 * time.Second):
		t.Fatal("workspace blocked its cancel handle on the handshake")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no handshake")
	}
	var closing sync.WaitGroup
	for i := 0; i < 16; i++ {
		closing.Add(1)
		go func() { defer closing.Done(); stream.Close() }()
	}
	closing.Wait()
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel a pending handshake")
	}
	if err := stream.Send(`{"type":"refresh"}`); err == nil {
		t.Fatal("closed connection accepted a send")
	}
	select {
	case event := <-events:
		t.Fatal("canceled handshake published", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestNativeWorkspaceHandshakeErrorsRetainStatusInAsyncCallbacks(t *testing.T) {
	for _, status := range []int{401, 403, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			events := make(eventRecorder, 4)
			stream, err := client.SubscribeWorkspace("csrf", "ja", events)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			select {
			case event := <-events:
				if event != [2]string{"transport_error", fmt.Sprintf(`{"status":%d}`, status)} {
					t.Fatal(event)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no handshake failure")
			}
		})
	}
}

func TestNativeWorkspaceDistinguishesPolicyExpiryFromOrdinaryDisconnect(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				defer cancel()
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
				code := websocket.StatusNormalClosure
				if policy {
					code = websocket.StatusPolicyViolation
				}
				conn.Close(code, "fixture")
			})
			events := make(eventRecorder, 4)
			stream, err := client.SubscribeWorkspace("csrf", "en", events)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			select {
			case event := <-events:
				kind := "transport_error"
				if policy {
					kind = "auth_expired"
				}
				if event[0] != kind {
					t.Fatal(event)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no workspace close event")
			}
		})
	}
}

func TestNativeWorkspaceHeartbeatsReachListenerAndKeepNativePong(t *testing.T) {
	pong := make(chan string, 1)
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
			return
		}
		_, data, err := conn.Read(ctx)
		if err == nil {
			pong <- string(data)
		}
	})
	events := make(eventRecorder, 4)
	stream, err := client.SubscribeWorkspace("csrf", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-events:
		if event != [2]string{"ping", "null"} {
			t.Fatal(event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("heartbeat was hidden from native watchdog")
	}
	select {
	case value := <-pong:
		if value != `{"type":"pong","visible":true}` {
			t.Fatal(value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native pong was lost")
	}
}

func TestNativeMultipartUpload(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chats/chat/messages" || r.Header.Get("X-CSRF-Token") != "csrf" {
			t.Error("upload origin or csrf lost")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if r.FormValue("message") != "hello" || header.Filename != "note.txt" || string(data) != "contents" {
			t.Error("multipart fields corrupted")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chat"}`)
	})
	payload, _ := json.Marshal(map[string]any{"fields": map[string]string{"message": "hello"}, "files": []map[string]string{{"name": "note.txt", "data": base64.StdEncoding.EncodeToString([]byte("contents"))}}})
	if _, err := client.Upload("chat", string(payload), "csrf", "en"); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"fields":{"bad":"field"}}`, `{"files":[{"name":"empty","data":""}]}`} {
		if _, err := client.Upload("chat", payload, "csrf", "en"); err == nil {
			t.Fatal("invalid upload accepted")
		}
	}
}

func TestNativeSessionPersistenceIsPrivateAndOriginScoped(t *testing.T) {
	directory := t.TempDir()
	origin, _ := url.Parse("https://spool.example.ts.net")
	jar, err := newSessionJar(directory, origin)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: "spool_session", Value: "private-session", Path: "/", Secure: true, HttpOnly: true, MaxAge: 60}})
	restored, err := newSessionJar(directory, origin)
	if err != nil {
		t.Fatal(err)
	}
	if cookies := restored.Cookies(origin); len(cookies) != 1 || cookies[0].Value != "private-session" {
		t.Fatal("native login was not retained")
	}
	info, err := os.Stat(jar.(*sessionJar).path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("session file not private", err)
	}
	other, _ := url.Parse("https://another.example.ts.net")
	different, err := newSessionJar(directory, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(different.Cookies(other)) != 0 {
		t.Fatal("another server inherited the login")
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: "spool_session", Path: "/", MaxAge: -1}})
	signedOut, err := newSessionJar(directory, origin)
	if err != nil {
		t.Fatal(err)
	}
	if len(signedOut.Cookies(origin)) != 0 {
		t.Fatal("logout did not clear the retained login")
	}
}
