package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnrollmentLimiterBudgetsPerNetwork(t *testing.T) {
	limiter := enrollmentLimiter{limit: 2, recent: make(map[string][]time.Time)}
	now := time.Unix(1_800_000_000, 0)
	enroll := func(l *enrollmentLimiter, ip string, at time.Time) bool {
		if !l.allowed(ip, at) {
			return false
		}
		l.add(ip, at)
		return true
	}
	for _, step := range []struct {
		ip   string
		want bool
		why  string
	}{
		{"203.0.113.1", true, "first enrollment"},
		{"203.0.113.1", true, "second enrollment"},
		{"203.0.113.1", false, "budget exceeded"},
		{"::ffff:203.0.113.1", false, "mapped form of the same address"},
		{"203.0.113.2", true, "another address"},
		{"2001:db8:1:2::1", true, "IPv6 network"},
		{"2001:db8:1:2:ffff::9", true, "same /64"},
		{"2001:db8:1:2::3", false, "the /64 shares one budget"},
		{"2001:db8:1:3::1", true, "another /64"},
		{"", true, "unresolved address"},
		{"not an address", true, "unresolved addresses share a budget"},
		{"", false, "unresolved budget exceeded"},
	} {
		if got := enroll(&limiter, step.ip, now); got != step.want {
			t.Fatalf("%s (%q): allowed %t, want %t", step.why, step.ip, got, step.want)
		}
	}
	if !enroll(&limiter, "203.0.113.1", now.Add(enrollmentWindow)) {
		t.Fatal("budget did not return after the window")
	}

	crowded := enrollmentLimiter{limit: 2, recent: make(map[string][]time.Time)}
	for i := range maxEnrollmentNetworks {
		if !enroll(&crowded, fmt.Sprintf("10.%d.%d.1", i/256, i%256), now) {
			t.Fatal("refused a network within the tracking bound")
		}
	}
	if enroll(&crowded, "192.0.2.1", now) {
		t.Fatal("tracked more networks than the bound")
	}
	if !enroll(&crowded, "192.0.2.1", now.Add(enrollmentWindow)) || len(crowded.recent) != 1 {
		t.Fatal("expired networks were not forgotten:", len(crowded.recent))
	}
}

func TestAutomaticEnrollmentIsLimitedPerNetwork(t *testing.T) {
	broker, err := NewServer(ServerOptions{AdminToken: testAdminToken, DataDir: t.TempDir(), AllowUnregisteredHosts: true, AutomaticRegistrationsPerHour: 2})
	if err != nil {
		t.Fatal(err)
	}
	var address atomic.Value
	address.Store("203.0.113.1:40000")
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = address.Load().(string)
		broker.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { broker.Close(); public.Close() })
	enroll := func(token string) (*Identity, error) {
		id, _ := NewIdentity()
		_, err := Register(t.Context(), public.URL, token, id, nil)
		return id, err
	}
	first, err := enroll("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enroll(""); err != nil {
		t.Fatal(err)
	}
	if _, err := enroll(""); !errors.Is(err, ErrRegistrationLimited) {
		t.Fatal("one network enrolled more identities than its budget:", err)
	}
	if got := len(broker.registrationStatuses("")); got != 2 {
		t.Fatal("a refused enrollment left a record:", got)
	}
	// Recovering a lost credential rotates the existing record.
	if _, err := Register(t.Context(), public.URL, "", first, nil); err != nil {
		t.Fatal("existing identity could not recover its credential:", err)
	}
	// An administrator decides who holds a token, so tokens are not limited.
	_, token := createViaAPI(t, broker, "issued")
	if _, err := enroll(token); err != nil {
		t.Fatal("issued token was limited:", err)
	}
	address.Store("198.51.100.7:40000")
	if _, err := enroll(""); err != nil {
		t.Fatal("another network shared the exhausted budget:", err)
	}
}

func TestAutomaticEnrollmentLeavesRoomForIssuedTokens(t *testing.T) {
	broker := testBrokerMode(t, true)
	broker.mu.Lock()
	for len(broker.registrations) < maxRegistrations-automaticRegistrationReserve {
		record := registrationRecord{Registration: Registration{ID: randomID(), Name: "filler", CreatedAt: time.Now().UTC()}, TokenHash: tokenDigest(randomID())}
		broker.registrations[record.ID] = record
		broker.tokenIndex[record.TokenHash] = record.ID
	}
	broker.mu.Unlock()
	public := httptest.NewServer(broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	id, _ := NewIdentity()
	if _, err := Register(t.Context(), public.URL, "", id, nil); err == nil {
		t.Fatal("automatic enrollment used the administrator's reserve")
	}
	if _, _, err := broker.CreateRegistration("issued"); err != nil {
		t.Fatal("administrator could not issue a token:", err)
	}
}

func TestPurgeRemovesRecordsAndTheirConnections(t *testing.T) {
	broker := testBrokerMode(t, true)
	public := httptest.NewServer(broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	connected, token := createViaAPI(t, broker, "connected")
	id, _ := NewIdentity()
	startAdminTestHost(t, public, token, id, make(chan struct{}))

	blocked, _ := NewIdentity()
	if _, err := Register(t.Context(), public.URL, "", blocked, nil); err != nil {
		t.Fatal(err)
	}
	var revoked Registration
	for _, status := range broker.registrationStatuses("") {
		if status.ServerID == blocked.ID() {
			revoked = status.Registration
		}
	}
	if err := broker.RevokeRegistration(revoked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.Context(), public.URL, "", blocked, nil); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatal("revoked identity enrolled again:", err)
	}

	for _, credential := range []string{"", token} {
		if w := adminRequest(t, broker, "POST", "/v1/registrations/purge", credential, `{"ids":["`+connected.ID+`"]}`); w.Code != http.StatusUnauthorized {
			t.Fatal("purge accepted without the administrator credential:", w.Code)
		}
	}
	tooMany, _ := json.Marshal(map[string][]string{"ids": strings.Split(strings.Repeat(connected.ID+" ", 257), " ")[:257]})
	for _, body := range []string{`{}`, `{"ids":[]}`, `{"ids":["not-an-id"]}`, `{"ids":["` + connected.ID + `"],"all":true}`, string(tooMany)} {
		if w := adminRequest(t, broker, "POST", "/v1/registrations/purge", testAdminToken, body); w.Code != http.StatusBadRequest {
			t.Fatal("purge accepted invalid input:", w.Code, body[:min(len(body), 60)])
		}
	}
	if w := adminRequest(t, broker, "GET", "/v1/registrations/purge", testAdminToken, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatal("purge accepted another method:", w.Code)
	}
	if got := len(broker.registrationStatuses("")); got != 2 {
		t.Fatal("a refused purge changed the store:", got)
	}

	body, _ := json.Marshal(map[string][]string{"ids": {connected.ID, revoked.ID, randomID()}})
	w := adminRequest(t, broker, "POST", "/v1/registrations/purge", testAdminToken, string(body))
	var result struct {
		Purged int `json:"purged"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Purged != 2 {
		t.Fatalf("purge returned %d: %s", w.Code, w.Body)
	}
	if got := len(broker.registrationStatuses("")); got != 0 {
		t.Fatal("purged records remain:", got)
	}
	if connectedHost(broker, id.ID()) != nil {
		t.Fatal("purged server stayed connected")
	}
	if _, err := Register(t.Context(), public.URL, token, id, nil); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatal("purged credential still accepted:", err)
	}
	// No tombstone remains, so open registration admits the identity again.
	if _, err := Register(t.Context(), public.URL, "", blocked, nil); err != nil {
		t.Fatal("purged identity could not enroll again:", err)
	}
	broker.Close()
	restored, err := NewServer(broker.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if list := restored.registrationStatuses(""); len(list) != 1 || list[0].ServerID != blocked.ID() || list[0].RevokedAt != nil {
		t.Fatal("purge was not persisted:", list)
	}
}
