package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestRandomRegistrationNamesAndBatchPersistence(t *testing.T) {
	s := testBroker(t)
	var issued []issuedRegistration
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"  \t "}`} {
		w := adminRequest(t, s, "POST", "/v1/registrations", testAdminToken, body)
		var result issuedRegistration
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("empty name: %d %s", w.Code, w.Body)
		}
		issued = append(issued, result)
	}
	for _, count := range []int{1, maxRegistrationBatch} {
		w := adminRequest(t, s, "POST", "/v1/registrations/batch", testAdminToken, fmt.Sprintf(`{"count":%d}`, count))
		var result struct {
			Registrations []issuedRegistration `json:"registrations"`
		}
		if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Registrations) != count {
			t.Fatalf("batch %d: %d %s", count, w.Code, w.Body)
		}
		issued = append(issued, result.Registrations...)
	}
	names, tokens, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	pattern := regexp.MustCompile(`^[A-Za-z]{12}$`)
	data, err := os.ReadFile(filepath.Join(s.opts.DataDir, "registrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	listed := adminRequest(t, s, "GET", "/v1/registrations", testAdminToken, "").Body.String()
	for _, result := range issued {
		name, token, id := result.Registration.Name, result.Token, result.Registration.ID
		if !pattern.MatchString(name) || names[name] || tokens[token] || ids[id] || !validToken(token) || !validToken(id) {
			t.Fatal("invalid or duplicate credential")
		}
		names[name], tokens[token], ids[id] = true, true, true
		if bytes.Contains(data, []byte(token)) || strings.Contains(listed, token) {
			t.Fatal("plaintext token was retained or listed")
		}
	}
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	for _, result := range issued {
		record := restored.registrations[result.Registration.ID]
		if record.Name != result.Registration.Name || record.TokenHash != tokenDigest(result.Token) || restored.tokenIndex[record.TokenHash] != record.ID {
			t.Fatal("batch credential not restored")
		}
	}
}

func TestRegistrationBatchValidationAndCapacity(t *testing.T) {
	s := testBroker(t)
	for _, token := range []string{"", "wrong"} {
		if w := adminRequest(t, s, "POST", "/v1/registrations/batch", token, `{"count":2}`); w.Code != 401 {
			t.Fatal("batch authorization bypassed")
		}
	}
	for _, body := range []string{`{}`, `{"count":null}`, `{"count":0}`, `{"count":-1}`, `{"count":101}`, `{"count":1.5}`, `{"count":"2"}`, `{"count":2,"name":"custom"}`} {
		if w := adminRequest(t, s, "POST", "/v1/registrations/batch", testAdminToken, body); w.Code != 400 {
			t.Fatalf("invalid batch accepted: %s: %d", body, w.Code)
		}
	}
	if w := adminRequest(t, s, "GET", "/v1/registrations/batch", testAdminToken, ""); w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal("batch method not enforced")
	}
	if len(s.registrations) != 0 || len(s.tokenIndex) != 0 {
		t.Fatal("invalid batch changed registry")
	}
	// Capacity is checked before allocating or persisting any records.
	for i := range maxRegistrations - 1 {
		s.registrations[fmt.Sprint(i)] = registrationRecord{}
	}
	if issued, err := s.createRegistrations(make([]string, 2)); err == nil || len(issued) != 0 || len(s.registrations) != maxRegistrations-1 || len(s.tokenIndex) != 0 {
		t.Fatal("over-capacity batch was partially published")
	}
}

func TestConcurrentRegistrationBatches(t *testing.T) {
	s := testBroker(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.createRegistrations(make([]string, 3)); err != nil {
				t.Error(err)
			}
			if _, _, err := s.CreateRegistration(""); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(s.registrationStatuses("")) != 32 || len(s.tokenIndex) != 32 {
		t.Fatal("concurrent issuance lost credentials")
	}
}
