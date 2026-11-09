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

// Each profile has one session. A transport projects it only onto its own exact
// HTTPS origin; credentials never pass through Java or a wildcard cookie domain.
type serverSessionJar struct {
	mu      sync.Mutex
	origin  *url.URL
	path    string
	origins []string
	cookie  *http.Cookie
	closed  bool
}

type savedServerSession struct {
	Origins []string     `json:"origins"`
	Cookie  *http.Cookie `json:"cookie"`
}

// UseServerSession binds a newly created transport to its server profile before
// any requests start. Both configured routes then reuse the same Spool login.
// The separate Tailscale node state remains in NewClient's data directory.
func (c *Client) UseServerSession(directory, directURL, tailscaleURL string) error {
	return c.useServerSession(directory, directURL, tailscaleURL)
}

// UsePairedServerSession includes the optional Relay identity in this profile's
// shared login. Admission credentials are checked by NewRelayClient, never by
// another transport's session binding. Older or invalid codes cannot disable
// Public HTTPS/Tailscale, and a readable legacy identity preserves login handoff.
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
	jar, err := newServerSessionJar(directory, c.origin, directURL, tailscaleURL, c.direct, extra...)
	if err != nil {
		return err
	}
	c.http.Jar = jar
	return nil
}

func newServerSessionJar(directory string, origin *url.URL, directURL, tailscaleURL string, direct bool, extra ...string) (*serverSessionJar, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("server session directory must be absolute")
	}
	jar := &serverSessionJar{origin: origin, path: filepath.Join(directory, "session.json")}
	for _, address := range append([]string{directURL, tailscaleURL}, extra...) {
		if address == "" {
			continue
		}
		endpoint, _, err := parseEndpoint(address)
		if err != nil {
			return nil, err
		}
		jar.origins = append(jar.origins, endpoint.String())
	}
	if !containsOrigin(jar.origins, origin.String()) {
		return nil, errors.New("connection does not belong to this server profile")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.Open(jar.path)
	if err == nil {
		var saved savedServerSession
		decodeErr := json.NewDecoder(io.LimitReader(file, 16384)).Decode(&saved)
		_ = file.Close()
		if decodeErr == nil && saved.Cookie != nil {
			for _, previous := range saved.Origins {
				if containsOrigin(jar.origins, previous) && validSharedSession(saved.Cookie) {
					jar.cookie = saved.Cookie
					break
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		// One-time upgrade: prefer the selected route, then the other configured
		// route. A persisted empty record prevents logout reviving old logins.
		legacy := []struct{ address, mode string }{{directURL, "https"}, {tailscaleURL, "tsnet"}}
		if !direct {
			legacy[0], legacy[1] = legacy[1], legacy[0]
		}
		for _, entry := range legacy {
			legacyOrigin, _, _ := parseEndpoint(entry.address)
			jar.cookie = readLegacySession(filepath.Join(filepath.Dir(directory), entry.mode), legacyOrigin)
			if jar.cookie != nil {
				break
			}
		}
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

func containsOrigin(origins []string, origin string) bool {
	for _, candidate := range origins {
		if candidate == origin {
			return true
		}
	}
	return false
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
	if json.NewDecoder(io.LimitReader(file, 8192)).Decode(&saved) != nil || saved.Origin != origin.String() || !validSession(&saved.Cookie, origin) || !validSharedSession(&saved.Cookie) {
		return nil
	}
	return &saved.Cookie
}

func validSharedSession(cookie *http.Cookie) bool {
	return cookie.Name == "spool_session" && cookie.Value != "" && len(cookie.Value) <= 4096 && cookie.Valid() == nil && cookie.Secure && cookie.HttpOnly && cookie.Path == "/" && cookie.Expires.After(time.Now())
}

func (j *serverSessionJar) matches(origin *url.URL) bool {
	return origin != nil && origin.Scheme == "https" && origin.Host == j.origin.Host && origin.User == nil
}

func (j *serverSessionJar) Cookies(origin *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || !j.matches(origin) || j.cookie == nil || !validSharedSession(j.cookie) {
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
		} else {
			saved := *cookie
			if saved.MaxAge > 0 {
				saved.Expires = time.Now().Add(time.Duration(saved.MaxAge) * time.Second)
			}
			if !validSharedSession(&saved) {
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
	data, err := json.Marshal(savedServerSession{Origins: j.origins, Cookie: j.cookie})
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
