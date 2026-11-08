package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testAdminToken = "test-administrator-credential-0000000001"

func testBroker(t *testing.T) *Server { return testBrokerMode(t, false) }

func testBrokerMode(t *testing.T, automatic bool) *Server {
	t.Helper()
	s, err := NewServer(ServerOptions{AdminToken: testAdminToken, DataDir: t.TempDir(), AllowUnregisteredHosts: automatic})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func adminRequest(t *testing.T, s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)
	return w
}

func createViaAPI(t *testing.T, s *Server, name string) (Registration, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	w := adminRequest(t, s, "POST", "/v1/registrations", testAdminToken, string(body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", w.Code, w.Body)
	}
	var result struct {
		Registration Registration `json:"registration"`
		Token        string       `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !validToken(result.Token) || result.Registration.ServerID != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid unused registration response")
	}
	return result.Registration, result.Token
}

func TestAdminAuthenticationAndPersistentRevocation(t *testing.T) {
	s := testBroker(t)
	for _, credential := range []string{"", "wrong"} {
		w := adminRequest(t, s, "POST", "/v1/registrations", credential, `{"name":"home"}`)
		if w.Code != 401 {
			t.Fatal("unprotected administration", w.Code)
		}
	}
	registration, token := createViaAPI(t, s, "home")
	if w := adminRequest(t, s, "GET", "/v1/registrations", token, ""); w.Code != 401 {
		t.Fatal("server token granted admin access")
	}
	r := httptest.NewRequest("GET", "/v1/registrations", nil)
	r.Header.Set("Authorization", "Bearer "+testAdminToken)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("administration exposed on public listener")
	}
	r.Header.Set("Origin", "https://untrusted.example")
	w = httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("accepted browser administration")
	}
	for _, path := range []string{"/v1/registrations?token=ignored", "/v1/registrations?"} {
		if w = adminRequest(t, s, "GET", path, testAdminToken, ""); w.Code != 400 {
			t.Fatal("accepted query credentials", w.Code)
		}
	}
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"home","server_id":"not-required"}`, `{"name":"home"} {}`, `{"name":"` + strings.Repeat("a", 5000) + `"}`} {
		if w = adminRequest(t, s, "POST", "/v1/registrations", testAdminToken, body); w.Code != 400 {
			t.Fatal("accepted invalid input", w.Code)
		}
	}
	for _, path := range []string{"/v1/registrations", "/v1/registrations/" + registration.ID} {
		w = adminRequest(t, s, "GET", path, testAdminToken, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "token_hash") {
			t.Fatal("credential exposed in metadata")
		}
	}
	file := filepath.Join(s.opts.DataDir, "registrations.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(token)) || bytes.Contains(data, []byte(testAdminToken)) || !bytes.Contains(data, []byte(tokenDigest(token))) {
		t.Fatal("store did not hash credentials")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("registration store is not private", err)
	}
	for i := 0; i < 2; i++ {
		w = adminRequest(t, s, "DELETE", "/v1/registrations/"+registration.ID, testAdminToken, "")
		if w.Code != 204 {
			t.Fatal("revocation not idempotent", w.Code)
		}
	}
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	status := restored.registrationStatuses(registration.ID)
	if len(status) != 1 || status[0].RevokedAt == nil {
		t.Fatal("revocation lost after restart")
	}
	r = httptest.NewRequest("GET", "/v1/host", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	restored.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("revoked credential accepted after restart", w.Code)
	}
	if w = adminRequest(t, restored, "DELETE", "/v1/registrations/"+randomID(), testAdminToken, ""); w.Code != 404 {
		t.Fatal("unknown revocation did not return 404")
	}
}

func startAdminTestHost(t *testing.T, public *httptest.Server, token string, id *Identity, closed chan struct{}) *testNetwork {
	t.Helper()
	host, err := NewHost(context.Background(), public.URL, token, id, public.Client())
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	host.OnStatus = func(state string) {
		if state == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	app := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			io.WriteString(w, "ready\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(closed)
			return
		}
		io.WriteString(w, id.ID())
	})}
	cfg, _ := id.TLSConfig()
	go app.Serve(tls.NewListener(host, cfg))
	done := make(chan struct{})
	go func() { host.Run(); host.Wait(); close(done) }()
	t.Cleanup(func() {
		host.Close()
		app.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("host did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not register")
	}
	return &testNetwork{host: host, app: app, hostDone: done, pair: Pairing{Version: 1, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken}}
}

func TestRevocationDisconnectsOnlyItsServerAndAllowsReplacement(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	first, firstToken := createViaAPI(t, s, "home")
	second, secondToken := createViaAPI(t, s, "office")
	if firstToken == secondToken {
		t.Fatal("shared registration credential")
	}
	firstID, _ := NewIdentity()
	secondID, _ := NewIdentity()
	firstClosed, secondClosed := make(chan struct{}), make(chan struct{})
	a := startAdminTestHost(t, public, firstToken, firstID, firstClosed)
	b := startAdminTestHost(t, public, secondToken, secondID, secondClosed)
	ca, da := a.client(t, a.pair)
	cb, _ := b.client(t, b.pair)
	openStream := func(c *http.Client, p Pairing) *http.Response {
		t.Helper()
		r, err := c.Get(Origin(p.ID()) + "/stream")
		if err != nil {
			t.Fatal(err)
		}
		first := make([]byte, 6)
		if _, err := io.ReadFull(r.Body, first); err != nil {
			r.Body.Close()
			t.Fatal(err)
		}
		return r
	}
	ra, rb := openStream(ca, a.pair), openStream(cb, b.pair)
	defer ra.Body.Close()
	defer rb.Body.Close()
	w := adminRequest(t, s, "GET", "/v1/registrations/"+first.ID, testAdminToken, "")
	var status registrationStatus
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || status.ServerID != firstID.ID() || !status.Connected || status.Connections != 1 {
		t.Fatal("missing live registration status", w.Body)
	}
	w = adminRequest(t, s, "DELETE", "/v1/registrations/"+first.ID, testAdminToken, "")
	if w.Code != 204 {
		t.Fatal("revocation failed", w.Body)
	}
	select {
	case <-firstClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked stream remained open")
	}
	select {
	case <-secondClosed:
		t.Fatal("revocation disconnected another server")
	default:
	}
	if request(t, cb, b.pair, "/") != secondID.ID() {
		t.Fatal("other server became unreachable")
	}
	if len(s.registrationStatuses(second.ID)) != 1 || !s.registrationStatuses(second.ID)[0].Connected {
		t.Fatal("other registration was revoked")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if conn, err := dialWebsocket(ctx, nil, public.URL, "/v1/host", firstToken); err == nil {
		conn.CloseNow()
		t.Fatal("revoked host reconnected")
	}
	_, replacement := createViaAPI(t, s, "home replacement")
	startAdminTestHost(t, public, replacement, firstID, make(chan struct{}))
	da.NetworkChanged()
	if request(t, ca, a.pair, "/") != firstID.ID() {
		t.Fatal("replacement required changing paired identity")
	}
}

func beginRegistration(t *testing.T, publicURL, token string) (*websocket.Conn, controlMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	c, err := dialWebsocket(ctx, nil, publicURL, "/v1/host", token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	challenge, err := readControl(ctx, c)
	if err != nil || challenge.Type != "challenge" {
		t.Fatal("missing challenge", err)
	}
	return c, challenge
}

func finishRegistration(ctx context.Context, c *websocket.Conn, challenge controlMessage, id *Identity) bool {
	err := writeControl(ctx, c, controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, Signature: ed25519.Sign(id.PrivateKey(), proof(challenge.Challenge, id.AccessToken))})
	if err != nil {
		return false
	}
	ready, err := readControl(ctx, c)
	return err == nil && ready.Type == "ready"
}

func TestFirstRegistrationClaimsOneIdentityAndPersistsIt(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	registration, token := createViaAPI(t, s, "one server")
	a, ac := beginRegistration(t, public.URL, token)
	b, bc := beginRegistration(t, public.URL, token)
	first, _ := NewIdentity()
	second, _ := NewIdentity()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	winners := make(chan string, 2)
	var workers sync.WaitGroup
	for _, entry := range []struct {
		c         *websocket.Conn
		challenge controlMessage
		id        *Identity
	}{{a, ac, first}, {b, bc, second}} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if finishRegistration(ctx, entry.c, entry.challenge, entry.id) {
				winners <- entry.id.ID()
			}
		}()
	}
	workers.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatal("token was not limited to one server identity", len(winners))
	}
	winner := <-winners
	a.CloseNow()
	b.CloseNow()
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := restored.registrationStatuses(registration.ID); len(got) != 1 || got[0].ServerID != winner {
		t.Fatal("server identity was not persisted")
	}
	server := httptest.NewServer(restored)
	defer server.Close()
	impostor := first
	if winner == first.ID() {
		impostor = second
	}
	c, challenge := beginRegistration(t, server.URL, token)
	if finishRegistration(ctx, c, challenge, impostor) {
		t.Fatal("stolen token registered another identity after restart")
	}
}

func TestRevocationDuringRegistrationCannotResurrectHost(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	registration, token := createViaAPI(t, s, "pending")
	c, challenge := beginRegistration(t, public.URL, token)
	w := adminRequest(t, s, "DELETE", "/v1/registrations/"+registration.ID, testAdminToken, "")
	if w.Code != 204 {
		t.Fatal("revocation failed")
	}
	id, _ := NewIdentity()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if finishRegistration(ctx, c, challenge, id) {
		t.Fatal("in-flight handshake bypassed revocation")
	}
	if got := s.registrationStatuses(registration.ID); len(got) != 1 || got[0].Connected || got[0].ServerID != "" {
		t.Fatal("revoked pending token claimed an identity")
	}
}

func TestStoreFailuresDoNotReportSuccessfulChanges(t *testing.T) {
	s := testBroker(t)
	registration, _ := createViaAPI(t, s, "existing")
	path := filepath.Join(s.opts.DataDir, "registrations.json")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	w := adminRequest(t, s, "POST", "/v1/registrations", testAdminToken, `{"name":"failed"}`)
	if w.Code != 500 || len(s.registrationStatuses("")) != 1 {
		t.Fatal("failed persistence created a usable token")
	}
	w = adminRequest(t, s, "DELETE", "/v1/registrations/"+registration.ID, testAdminToken, "")
	if w.Code != 500 || s.registrationStatuses(registration.ID)[0].RevokedAt != nil {
		t.Fatal("failed persistence reported a revocation")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if other, err := NewServer(s.opts); err == nil {
		other.Close()
		t.Fatal("corrupt state silently erased revocations")
	}
}

func exchangeRegistration(t *testing.T, publicURL, token string, id *Identity) (controlMessage, error) {
	t.Helper()
	c, challenge := beginRegistration(t, publicURL, token)
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := writeControl(ctx, c, controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, Signature: ed25519.Sign(id.PrivateKey(), proof(challenge.Challenge, id.AccessToken))})
	if err != nil {
		return controlMessage{}, err
	}
	return readControl(ctx, c)
}

func TestAutomaticEnrollmentIssuesAndSavesOneToken(t *testing.T) {
	s := testBrokerMode(t, true)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	id, _ := NewIdentity()
	n := startAdminTestHost(t, public, "", id, make(chan struct{}))
	token := n.host.RegistrationToken()
	if !validToken(token) {
		t.Fatal("host did not retain automatically issued token")
	}
	list := s.registrationStatuses("")
	if len(list) != 1 || !list[0].Automatic || list[0].ServerID != id.ID() || !list[0].Connected {
		t.Fatal("identity was not associated with automatic registration")
	}
	n.host.Close()
	select {
	case <-n.hostDone:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic host did not stop")
	}
	s.Close()
	// The issued token continues to work after restarting in token-required mode.
	opts := s.opts
	opts.AllowUnregisteredHosts = false
	restored, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(restored)
	t.Cleanup(func() { restored.Close(); server.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, wrong := range []string{"", testAdminToken} {
		if c, err := dialWebsocket(ctx, nil, server.URL, "/v1/host", wrong); err == nil {
			c.CloseNow()
			t.Fatal("required mode accepted missing/administrator credential")
		}
	}
	ready, err := exchangeRegistration(t, server.URL, token, id)
	if err != nil || ready.Type != "ready" || ready.RegistrationToken != "" {
		t.Fatal("saved token failed in required mode", err)
	}
	if len(restored.registrationStatuses("")) != 1 {
		t.Fatal("reconnect created another token record")
	}
}

func TestAutomaticRegistrationRevocationSurvivesRestart(t *testing.T) {
	s := testBrokerMode(t, true)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	id, _ := NewIdentity()
	closed := make(chan struct{})
	n := startAdminTestHost(t, public, "", id, closed)
	client, _ := n.client(t, n.pair)
	response, err := client.Get(Origin(id.ID()) + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, 6)
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	token := n.host.RegistrationToken()
	registration := s.registrationStatuses("")[0]
	w := adminRequest(t, s, "DELETE", "/v1/registrations/"+registration.ID, testAdminToken, "")
	if w.Code != 204 {
		t.Fatal("automatic registration could not be revoked")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic host stream survived revocation")
	}
	n.host.Close()
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(restored)
	t.Cleanup(func() { restored.Close(); server.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if c, err := dialWebsocket(ctx, nil, server.URL, "/v1/host", token); err == nil {
		c.CloseNow()
		t.Fatal("revoked automatic token accepted")
	}
	if ready, err := exchangeRegistration(t, server.URL, "", id); err == nil && ready.Type == "ready" {
		t.Fatal("clearing token bypassed revocation")
	}
	if len(restored.registrationStatuses("")) != 1 {
		t.Fatal("revoked identity enrolled another credential")
	}
	_, replacement := createViaAPI(t, restored, "approved replacement")
	if ready, err := exchangeRegistration(t, server.URL, replacement, id); err != nil || ready.Type != "ready" {
		t.Fatal("administrator could not reauthorize identity", err)
	}
}

func TestAutomaticEnrollmentRequiresProofAndRetriesLostCredential(t *testing.T) {
	s := testBrokerMode(t, true)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	id, _ := NewIdentity()
	c, _ := beginRegistration(t, public.URL, "")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := writeControl(ctx, c, controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, Signature: make([]byte, ed25519.SignatureSize)}); err != nil {
		t.Fatal(err)
	}
	if ready, err := readControl(ctx, c); err == nil && ready.Type == "ready" {
		t.Fatal("automatic registration accepted unsigned identity")
	}
	c.CloseNow()
	if len(s.registrationStatuses("")) != 0 {
		t.Fatal("forged identity created a token")
	}
	ready, err := exchangeRegistration(t, public.URL, "", id)
	if err != nil || !validToken(ready.RegistrationToken) {
		t.Fatal("automatic credential missing", err)
	}
	first := ready.RegistrationToken
	deadline := time.Now().Add(3 * time.Second)
	for s.registrationStatuses("")[0].Connected && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// The first ready reply was not saved. Retry using the same proven identity.
	ready, err = exchangeRegistration(t, public.URL, "", id)
	if err != nil || !validToken(ready.RegistrationToken) || ready.RegistrationToken == first {
		t.Fatal("lost credential did not recover", err)
	}
	if len(s.registrationStatuses("")) != 1 {
		t.Fatal("retry grew the registration list")
	}
	if conn, err := dialWebsocket(ctx, nil, public.URL, "/v1/host", first); err == nil {
		conn.CloseNow()
		t.Fatal("old credential survived recovery rotation")
	}
}

func TestHostWaitsForAutomaticCredentialPersistence(t *testing.T) {
	s := testBrokerMode(t, true)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	id, _ := NewIdentity()
	host, err := NewHost(t.Context(), public.URL, "", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	var received string
	host.OnRegistrationToken = func(token string) error { received = token; return os.ErrPermission }
	host.OnStatus = func(state string) {
		if state == "connected" {
			t.Error("host became connected before saving token")
		}
	}
	host.session()
	if !validToken(received) || host.RegistrationToken() != "" {
		t.Fatal("persistence failure retained an unsaved credential")
	}
}
