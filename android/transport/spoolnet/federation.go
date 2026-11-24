package spoolnet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"spool/relay"
)

const federationResponseLimit = 128 << 10

var errFederation = errors.New("federated sign-in was not available")

// FederationRelayRoute obtains tunnel routing only for this source account's
// currently authorized target. The server's private key never leaves it.
func (c *Client) FederationRelayRoute(target, account, language string) (string, error) {
	if !federationID(target) || account == "" {
		return "", errFederation
	}
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	var source struct {
		User struct {
			ID     string
			Source string `json:"source_server_id"`
		} `json:"user"`
		CSRF    string `json:"csrf"`
		Targets []struct {
			ID   string
			Mode string `json:"client_mode"`
		} `json:"federation_targets"`
	}
	response, err := c.request(ctx, http.MethodGet, "/api/me", nil, "", "", language)
	if err != nil || !federationJSON(response, &source) || source.User.ID != account || source.User.Source != "" || source.CSRF == "" {
		return "", errFederation
	}
	allowed := false
	for _, peer := range source.Targets {
		if peer.ID == target && peer.Mode == "relay" {
			allowed = true
		}
	}
	if !allowed {
		return "", errFederation
	}
	payload, _ := json.Marshal(map[string]string{"target": target})
	response, err = c.request(ctx, http.MethodPost, "/api/federation/relay-route", bytes.NewReader(payload), "application/json", source.CSRF, language)
	var result struct {
		Code string `json:"relay_code"`
	}
	if err != nil || !federationJSON(response, &result) {
		return "", errFederation
	}
	route, err := relay.ParsePairing(result.Code)
	if err != nil || route.ID() != target || route.Invitation != "" || len(route.LAN) != 0 {
		return "", errFederation
	}
	return result.Code, nil
}

// FederationSession keeps the target's session cookie inside Go until the
// Activity confirms that the source and destination are still current.
type FederationSession struct {
	target   *Client
	jar      *serverSessionJar
	previous uint64
	cookie   *http.Cookie
	profile  string
}

// Activate installs only the new session for the original destination. An
// independently signed-in account, logout, or retired transport wins instead.
func (s *FederationSession) Activate() (string, error) {
	if s.target.ctx.Err() != nil {
		return "", errFederation
	}
	if s.cookie != nil {
		if !s.jar.adoptFederation(s.previous, s.cookie) {
			return "", errFederation
		}
	} else if s.target.ctx.Err() != nil || !s.jar.federationCurrent(s.previous) {
		return "", errFederation
	}
	return s.profile, nil
}

func federationID(id string) bool {
	return len(id) == 52 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz234567") == ""
}

func federationJSON(response *http.Response, value any) bool {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, federationResponseLimit+1))
	return err == nil && len(data) <= federationResponseLimit && json.Unmarshal(data, value) == nil
}

// PrepareFederatedSession uses only the two already bound transports. No ticket
// is returned through gomobile, and the exchange has no cookie jar or redirects.
func (c *Client) PrepareFederatedSession(destination *Client, target, account, language string) (*FederationSession, error) {
	if destination == nil || destination == c || !federationID(target) || account == "" {
		return nil, errFederation
	}
	jar, ok := destination.http.Jar.(*serverSessionJar)
	if !ok {
		return nil, errFederation
	}
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(destination.ctx, cancel)
	defer stop()

	var source struct {
		User    struct{ ID string } `json:"user"`
		CSRF    string              `json:"csrf"`
		Targets []struct {
			ID  string `json:"id"`
			URL string `json:"client_url"`
		} `json:"federation_targets"`
	}
	response, err := c.request(ctx, http.MethodGet, "/api/me", nil, "", "", language)
	if err != nil || !federationJSON(response, &source) || source.User.ID != account || source.CSRF == "" {
		return nil, errFederation
	}
	allowed := false
	for _, peer := range source.Targets {
		if peer.ID != target {
			continue
		}
		origin, _, parseErr := parseEndpoint(peer.URL)
		allowed = destination.relay != nil && destination.relay.Pairing.ID() == target ||
			destination.relay == nil && parseErr == nil && origin.String() == destination.origin.String()
		break
	}
	if !allowed {
		return nil, errFederation
	}
	anonymous := *destination.http
	anonymous.Jar = nil
	var version struct {
		ID string `json:"server_id"`
	}
	response, err = destination.requestWithHTTP(ctx, &anonymous, http.MethodGet, "/api/version", nil, "", "", language)
	if err != nil || !federationJSON(response, &version) || version.ID != target {
		return nil, errFederation
	}
	previous := jar.federationState()
	response, err = destination.request(ctx, http.MethodGet, "/api/me", nil, "", "", language)
	if err != nil {
		return nil, errFederation
	}
	result := &FederationSession{target: destination, jar: jar, previous: previous}
	if response.StatusCode == http.StatusOK {
		var profile json.RawMessage
		if !federationJSON(response, &profile) || !validFederationProfile(profile) {
			return nil, errFederation
		}
		result.profile = string(profile)
		return result, nil
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return nil, errFederation
	}
	var issued struct {
		Ticket string `json:"ticket"`
	}
	payload, _ := json.Marshal(map[string]string{"target": target})
	response, err = c.request(ctx, http.MethodPost, "/api/federation/issue", bytes.NewReader(payload), "application/json", source.CSRF, language)
	if err != nil || !federationJSON(response, &issued) || !validFederationTicket(issued.Ticket) {
		return nil, errFederation
	}
	payload, _ = json.Marshal(map[string]string{"ticket": issued.Ticket})
	response, err = destination.requestWithHTTP(ctx, &anonymous, http.MethodPost, "/api/federation/exchange", bytes.NewReader(payload), "application/json", "", language)
	if err != nil {
		return nil, errFederation
	}
	cookies := response.Cookies()
	var profile json.RawMessage
	if !federationJSON(response, &profile) || !validFederationProfile(profile) {
		return nil, errFederation
	}
	for _, cookie := range cookies {
		if cookie.MaxAge < 0 || !validSession(cookie, destination.origin) || !validProfileSession(cookie) {
			continue
		}
		if result.cookie != nil {
			return nil, errFederation
		}
		result.cookie = cookie
	}
	if result.cookie == nil {
		return nil, errFederation
	}
	result.profile = string(profile)
	return result, nil
}

func validFederationProfile(profile json.RawMessage) bool {
	var signedIn struct {
		User struct{ ID string } `json:"user"`
		CSRF string              `json:"csrf"`
	}
	return json.Unmarshal(profile, &signedIn) == nil && signedIn.User.ID != "" && signedIn.CSRF != ""
}

func validFederationTicket(ticket string) bool {
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 || parts[0] != "spool1" || !federationID(parts[1]) || len(parts[2]) != 43 {
		return false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	return err == nil && len(secret) == 32
}
