package relay

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAdminActivityCredentialRecovery(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		t.Run(fmt.Sprintf("automatic=%t", automatic), func(t *testing.T) {
			s := testBrokerMode(t, true)
			public := httptest.NewServer(s)
			t.Cleanup(public.Close)
			identity, err := NewIdentity()
			if err != nil {
				t.Fatal(err)
			}
			var initialToken string
			if !automatic {
				_, initialToken = createViaAPI(t, s, "recovered-server")
			}
			first, err := RegisterNamed(t.Context(), public.URL, initialToken, "recovered-server", identity, public.Client())
			if err != nil {
				t.Fatal(err)
			}
			record := s.registrationStatuses("")[0]
			activity := func(server *Server) []activityEvent {
				t.Helper()
				w := adminRequest(t, server, "POST", "/v1/activity/search", testAdminToken, `{"page":1,"page_size":100}`)
				var page struct{ Events []activityEvent }
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
					t.Fatalf("invalid activity response: %d %s", w.Code, w.Body)
				}
				return page.Events
			}
			before := activity(s)

			// A failed snapshot must publish neither a new credential nor a
			// successful recovery event.
			store := filepath.Join(s.opts.DataDir, "registrations.json")
			if err := os.Rename(store, store+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(store, 0700); err != nil {
				t.Fatal(err)
			}
			if token, err := Register(t.Context(), public.URL, "", identity, public.Client()); err == nil || token != "" {
				t.Fatal("failed persistence returned a recovered credential")
			}
			if !slices.Equal(before, activity(s)) {
				t.Fatal("failed persistence published a successful recovery event")
			}
			if token, err := Register(t.Context(), public.URL, first, identity, public.Client()); err != nil || token != first {
				t.Fatal("failed recovery invalidated the existing credential", err)
			}
			if err := os.Remove(store); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(store+".saved", store); err != nil {
				t.Fatal(err)
			}

			second, err := Register(t.Context(), public.URL, "", identity, public.Client())
			if err != nil || !validToken(second) || first == second {
				t.Fatal("recovery did not rotate the credential", err)
			}
			after := activity(s)
			if len(after) != len(before)+1 || !slices.Equal(after[1:], before) {
				t.Fatalf("recovery did not append exactly one event: %+v", after)
			}
			event := after[0]
			if event.Action != "rotate" || event.Result != "success" || event.TargetID != record.ID || event.Name != record.Name || !validToken(event.ID) || event.Time.IsZero() {
				t.Fatalf("invalid recovery event: %+v", event)
			}
			if _, err := Register(t.Context(), public.URL, first, identity, public.Client()); err == nil {
				t.Fatal("recovery left the old credential valid")
			}
			if token, err := Register(t.Context(), public.URL, second, identity, public.Client()); err != nil || token != second {
				t.Fatal("recovered credential was rejected", err)
			}
			if !slices.Equal(after, activity(s)) {
				t.Fatal("confirming an unchanged credential added a recovery event")
			}

			public.Close()
			s.Close()
			restored, err := NewServer(s.opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { restored.Close() })
			if !slices.Equal(after, activity(restored)) {
				t.Fatal("recovery activity was not persisted")
			}
			restarted := httptest.NewServer(restored)
			t.Cleanup(restarted.Close)
			if _, err := Register(t.Context(), restarted.URL, first, identity, restarted.Client()); err == nil {
				t.Fatal("restart restored the old credential")
			}
			if token, err := Register(t.Context(), restarted.URL, second, identity, restarted.Client()); err != nil || token != second {
				t.Fatal("recovered credential was not persisted", err)
			}
			data, err := json.Marshal(after)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{first, second, tokenDigest(first), tokenDigest(second), testAdminToken} {
				if strings.Contains(string(data), secret) {
					t.Fatal("recovery activity exposed a credential")
				}
			}
		})
	}
}

func TestAdminRegistrationSearch(t *testing.T) {
	s := testBroker(t)
	var records []Registration
	for i := range 25 {
		r, _ := createViaAPI(t, s, fmt.Sprintf("device-%02d", i))
		records = append(records, r)
	}
	s.mu.Lock()
	r := s.registrations[records[0].ID]
	r.ServerID = strings.Repeat("a", 52)
	r.RegistrationIP = "203.0.113.42"
	r.WebBlocked = true
	s.registrations[r.ID] = r
	s.mu.Unlock()
	if err := s.RevokeRegistration(records[1].ID); err != nil {
		t.Fatal(err)
	}
	input := registrationSearch{Page: 1, PageSize: 10, Group: "all", Connection: "all", Web: "all", Selected: []string{records[0].ID, randomID()}, DetailID: records[0].ID}
	type result struct {
		Registrations      []registrationStatus `json:"registrations"`
		Page, Pages, Total int
		Counts, Metrics    map[string]int
		Selected           []registrationStatus
		Detail             *registrationStatus
	}
	search := func() result {
		t.Helper()
		body, _ := json.Marshal(input)
		w := adminRequest(t, s, "POST", "/v1/registrations/search", testAdminToken, string(body))
		if w.Code != 200 {
			t.Fatalf("search: %d %s", w.Code, w.Body)
		}
		var output result
		if err := json.Unmarshal(w.Body.Bytes(), &output); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), "token_hash") {
			t.Fatal("search exposed credentials")
		}
		return output
	}
	output := search()
	if len(output.Registrations) != 10 || output.Registrations[0].ID != records[24].ID || output.Total != 25 || output.Pages != 3 || output.Counts["registered"] != 1 || output.Counts["unused"] != 23 || output.Counts["revoked"] != 1 || output.Metrics["active"] != 24 {
		t.Fatalf("incorrect first page: %+v", output)
	}
	if len(output.Selected) != 1 || output.Detail == nil || output.Detail.ID != records[0].ID {
		t.Fatal("cross-page metadata missing")
	}
	input.Page = 1000000000
	output = search()
	if output.Page != 3 || len(output.Registrations) != 5 || output.Registrations[4].ID != records[0].ID {
		t.Fatal("last page not clamped")
	}
	input.Query = "DEVICE-02"
	output = search()
	if output.Total != 1 || output.Registrations[0].ID != records[2].ID || output.Page != 1 {
		t.Fatal("case-insensitive search failed")
	}
	input.Query, input.Connection, input.Web = "203.0.113", "offline", "blocked"
	if output = search(); output.Total != 1 || output.Registrations[0].ID != records[0].ID {
		t.Fatal("combined filters failed")
	}
	input.Query = strings.ToUpper(r.ServerID)
	if output = search(); output.Total != 1 {
		t.Fatal("device ID search failed")
	}
	input.Connection = "online"
	if output = search(); output.Total != 0 || output.Pages != 1 || len(output.Registrations) != 0 {
		t.Fatal("empty page failed")
	}
	for _, body := range []string{
		`{}`, `{"page":1,"page_size":101}`, `{"page":0,"page_size":20}`, `{"page":1,"page_size":20,"group":"bogus"}`,
		`{"page":1,"page_size":20,"group":"all","connection":"all","web":"all","detail_id":"invalid"}`,
		`{"page":1,"page_size":20,"group":"all","connection":"all","web":"all","token":"secret"}`,
	} {
		if w := adminRequest(t, s, "POST", "/v1/registrations/search", testAdminToken, body); w.Code != 400 {
			t.Fatalf("accepted invalid search %s: %d", body, w.Code)
		}
	}
	if w := adminRequest(t, s, "POST", "/v1/registrations/search", "", `{}`); w.Code != 401 {
		t.Fatal("unprotected search")
	}
}

func TestAdminActivityPersistenceAndPurge(t *testing.T) {
	s := testBroker(t)
	empty := adminRequest(t, s, "POST", "/v1/activity/search", testAdminToken, `{"page":99,"page_size":20}`)
	var emptyPage struct {
		Events             []activityEvent
		Page, Pages, Total int
	}
	if empty.Code != 200 || json.Unmarshal(empty.Body.Bytes(), &emptyPage) != nil || emptyPage.Events == nil || len(emptyPage.Events) != 0 || emptyPage.Page != 1 || emptyPage.Pages != 1 || emptyPage.Total != 0 {
		t.Fatalf("invalid empty activity page: %s", empty.Body)
	}
	r, token := createViaAPI(t, s, "history-target")
	for _, blocked := range []bool{true, false} {
		body := fmt.Sprintf(`{"web_blocked":%t}`, blocked)
		if w := adminRequest(t, s, "PATCH", "/v1/registrations/"+r.ID, testAdminToken, body); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	if w := adminRequest(t, s, "DELETE", "/v1/registrations/"+r.ID, testAdminToken, ""); w.Code != 204 {
		t.Fatal(w.Body)
	}
	if w := adminRequest(t, s, "PATCH", "/v1/registrations/"+r.ID, testAdminToken, `{"web_blocked":true}`); w.Code != 404 {
		t.Fatal("expected failed operation")
	}
	body, _ := json.Marshal(map[string]any{"ids": []string{r.ID}})
	if w := adminRequest(t, s, "POST", "/v1/registrations/purge", testAdminToken, string(body)); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if len(s.registrationStatuses(r.ID)) != 0 {
		t.Fatal("purge retained registration")
	}
	if len(s.activity) != 6 {
		t.Fatalf("unexpected activity: %+v", s.activity)
	}
	actions := []string{}
	for _, event := range s.activity {
		actions = append(actions, event.Action)
		if event.TargetID != r.ID || event.Name != r.Name || !validToken(event.ID) || event.Time.IsZero() {
			t.Fatal("incomplete activity", event)
		}
	}
	if !slices.Equal(actions, []string{"generate", "disable", "allow", "revoke", "disable", "purge"}) || s.activity[4].Result != "failed" {
		t.Fatalf("missing activity results: %+v", s.activity)
	}
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	w := adminRequest(t, restored, "POST", "/v1/activity/search", testAdminToken, `{"page":1,"page_size":2}`)
	var page struct {
		Events             []activityEvent
		Total, Page, Pages int
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 2 || page.Total != 6 || page.Pages != 3 || page.Events[0].Action != "purge" {
		t.Fatalf("activity not restored/paginated: %s", w.Body)
	}
	data, err := os.ReadFile(filepath.Join(s.opts.DataDir, "registrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{token, tokenDigest(token), testAdminToken} {
		if strings.Contains(w.Body.String(), secret) || strings.Contains(string(data), secret) {
			t.Fatal("purged credential retained in activity")
		}
	}
	if w := adminRequest(t, restored, "POST", "/v1/activity/search", "", `{"page":1,"page_size":2}`); w.Code != 401 {
		t.Fatal("unprotected activity")
	}
	if w := adminRequest(t, restored, "POST", "/v1/activity/search", testAdminToken, `{"page":1,"page_size":101}`); w.Code != 400 {
		t.Fatal("unbounded activity page")
	}
}

func TestActivityBoundedAndAtomic(t *testing.T) {
	s := testBroker(t)
	r, _ := createViaAPI(t, s, "atomic-target")
	s.mu.Lock()
	for range maxActivityEvents + 10 {
		s.activity = append(s.activity, activityEvent{ID: randomID(), Time: time.Now().UTC(), Action: "generate", TargetID: r.ID, Name: r.Name, Result: "success"})
	}
	s.mu.Unlock()
	if err := s.RevokeRegistration(r.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.activity) != maxActivityEvents || s.activity[len(s.activity)-1].Action != "revoke" {
		t.Fatal("activity not bounded")
	}
	other, _ := createViaAPI(t, s, "write-failure")
	before := slices.Clone(s.activity)
	store := filepath.Join(s.opts.DataDir, "registrations.json")
	if err := os.Rename(store, store+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeRegistration(other.ID); err == nil {
		t.Fatal("expected storage failure")
	}
	if s.registrationStatuses(other.ID)[0].RevokedAt != nil || !slices.Equal(before, s.activity) {
		t.Fatal("failed write published a state change or successful activity")
	}
}
