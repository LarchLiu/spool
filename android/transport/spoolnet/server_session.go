package spoolnet

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"spool/relay"
)

// Each connection mode retains its own exact-origin session. The relay's stable
// origin represents its pinned server key on both LAN and relayed connections.
// Sharing a profile or a configured address never authorizes another origin.
type serverSessionJar struct {
	mu       sync.Mutex
	origin   *url.URL
	path     string
	mode     string
	sessions map[string]savedSession
	cookie   *http.Cookie
	closed   bool
}

type savedServerSessions struct {
	Version  int                     `json:"version"`
	Sessions map[string]savedSession `json:"sessions"`
}

// UseServerSession binds a newly created transport to its server profile before
// any requests start. Public HTTPS and Tailscale keep independent logins; changing
// an address requires signing in there. Tailscale node state remains separate.
func (c *Client) UseServerSession(directory, directURL, tailscaleURL string) error {
	return c.useServerSession(directory, directURL, tailscaleURL)
}

// UsePairedServerSession also binds the Relay session to its pinned identity.
// Admission credentials are checked by NewRelayClient, never by this binding.
// Older or invalid codes cannot disable independent Public HTTPS/Tailscale use.
func (c *Client) UsePairedServerSession(directory, directURL, tailscaleURL, code string) error {
	return c.useServerSession(directory, directURL, tailscaleURL, relaySessionOrigin(code))
}

func relaySessionOrigin(code string) string {
	code = strings.TrimSpace(code)
	if len(code) > 8192 || !strings.HasPrefix(code, "spool-pair:") {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, "spool-pair:"))
	var identity struct {
		Version   int    `json:"v"`
		PublicKey []byte `json:"key"`
	}
	if err != nil || json.Unmarshal(data, &identity) != nil ||
		(identity.Version != 1 && identity.Version != 2) || len(identity.PublicKey) != ed25519.PublicKeySize {
		return ""
	}
	return relay.Origin(relay.ServerID(identity.PublicKey))
}

func (c *Client) useServerSession(directory, directURL, tailscaleURL string, extra ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("connection is closed")
	}
	if _, bound := c.http.Jar.(*serverSessionJar); bound {
		return errors.New("server session is already configured")
	}
	mode := "tsnet"
	if c.direct {
		mode = "https"
	}
	if c.relay != nil {
		mode = "relay"
	}
	jar, err := newServerSessionJar(directory, c.origin, directURL, tailscaleURL, mode, extra...)
	if err != nil {
		return err
	}
	c.http.Jar = jar
	return nil
}

func newServerSessionJar(directory string, origin *url.URL, directURL, tailscaleURL, mode string, extra ...string) (*serverSessionJar, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("server session directory must be absolute")
	}
	jar := &serverSessionJar{origin: origin, path: filepath.Join(directory, "session.json"), mode: mode, sessions: make(map[string]savedSession)}
	addresses := map[string]string{"https": directURL, "tsnet": tailscaleURL}
	if len(extra) > 0 {
		addresses["relay"] = extra[0]
	}
	for key, address := range addresses {
		if address == "" {
			continue
		}
		endpoint, _, err := parseEndpoint(address)
		if err != nil {
			return nil, err
		}
		addresses[key] = endpoint.String()
	}
	if origin == nil || addresses[mode] == "" || addresses[mode] != origin.String() {
		return nil, errors.New("connection does not belong to this server profile")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.Open(jar.path)
	if err == nil {
		var saved savedServerSessions
		decoder := json.NewDecoder(io.LimitReader(file, 32768))
		decodeErr := decoder.Decode(&saved)
		if decodeErr == nil && decoder.Decode(new(any)) != io.EOF {
			decodeErr = errors.New("invalid server sessions record")
		}
		_ = file.Close()
		// The old shared record cannot prove which origin issued its cookie.
		// Do not expand its claimed Origins into per-route authorizations or fall
		// back to older files: an upgrade requires a fresh login on each route.
		if decodeErr == nil && saved.Version == 2 {
			for key, address := range addresses {
				entry, ok := saved.Sessions[key]
				endpoint, _, _ := parseEndpoint(address)
				if ok && endpoint != nil && entry.Origin == address && validProfileSession(&entry.Cookie) && validSession(&entry.Cookie, endpoint) {
					jar.sessions[key] = entry
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		// Before shared-profile sessions existed, files recorded their issuing
		// mode and exact origin. Migrate each only to that same binding, once.
		for key, address := range addresses {
			legacyOrigin, _, _ := parseEndpoint(address)
			cookie := readLegacySession(filepath.Join(filepath.Dir(directory), key), legacyOrigin)
			if cookie != nil {
				jar.sessions[key] = savedSession{Origin: address, Cookie: *cookie}
			}
		}
	}
	if entry, ok := jar.sessions[mode]; ok {
		jar.cookie = &entry.Cookie
	}
	if jar.cookie != nil {
		jar.cookie.Domain = ""
		jar.cookie.MaxAge = 0
	}
	if err := jar.persist(); err != nil {
		return nil, err
	}
	return jar, nil
}

func readLegacySession(directory string, origin *url.URL) *http.Cookie {
	if origin == nil {
		return nil
	}
	hash := sha256.Sum256([]byte(origin.String()))
	file, err := os.Open(filepath.Join(directory, "sessions", hex.EncodeToString(hash[:])+".json"))
	if err != nil {
		return nil
	}
	defer file.Close()
	var saved savedSession
	if json.NewDecoder(io.LimitReader(file, 8192)).Decode(&saved) != nil || saved.Origin != origin.String() || !validSession(&saved.Cookie, origin) || !validProfileSession(&saved.Cookie) {
		return nil
	}
	return &saved.Cookie
}

func validProfileSession(cookie *http.Cookie) bool {
	return cookie.Name == "spool_session" && cookie.Value != "" && len(cookie.Value) <= 4096 && cookie.Valid() == nil && cookie.Secure && cookie.HttpOnly && cookie.Path == "/" && cookie.Expires.After(time.Now())
}

func (j *serverSessionJar) matches(origin *url.URL) bool {
	return origin != nil && origin.Scheme == "https" && origin.Host == j.origin.Host && origin.User == nil
}

func (j *serverSessionJar) Cookies(origin *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || !j.matches(origin) || j.cookie == nil || !validProfileSession(j.cookie) {
		return nil
	}
	return []*http.Cookie{{Name: j.cookie.Name, Value: j.cookie.Value}}
}

func (j *serverSessionJar) SetCookies(origin *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || !j.matches(origin) {
		return
	}
	for _, cookie := range cookies {
		if cookie.Name != "spool_session" || cookie.Path != "/" || cookie.Domain != "" && cookie.Domain != j.origin.Hostname() {
			continue
		}
		if cookie.MaxAge < 0 || !cookie.Expires.IsZero() && !cookie.Expires.After(time.Now()) {
			j.cookie = nil
			// Signing out forgets every route in this profile. Keep an empty v2
			// record so switching networks cannot revive an older session file.
			clear(j.sessions)
		} else {
			saved := *cookie
			if saved.MaxAge > 0 {
				saved.Expires = time.Now().Add(time.Duration(saved.MaxAge) * time.Second)
			}
			if !validProfileSession(&saved) {
				continue
			}
			saved.Domain = ""
			saved.MaxAge = 0
			j.cookie = &saved
		}
		_ = j.persist()
	}
}

func (j *serverSessionJar) persist() error {
	if j.cookie == nil {
		delete(j.sessions, j.mode)
	} else {
		j.sessions[j.mode] = savedSession{Origin: j.origin.String(), Cookie: *j.cookie}
	}
	data, err := json.Marshal(savedServerSessions{Version: 2, Sessions: j.sessions})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(j.path), "session-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), j.path)
}

func (j *serverSessionJar) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	// A response from the retired network must not overwrite a newer login or logout.
	j.closed = true
}
