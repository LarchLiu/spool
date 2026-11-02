package spoolnet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Only Spool's secure session cookie is retained. Files live beside tsnet's
// credentials in Android's private, non-backed-up storage, scoped by exact origin.
type sessionJar struct {
	inner  http.CookieJar
	origin *url.URL
	path   string
	mu     sync.Mutex
}

type savedSession struct {
	Origin string      `json:"origin"`
	Cookie http.Cookie `json:"cookie"`
}

func newSessionJar(dataDir string, origin *url.URL) (http.CookieJar, error) {
	directory := filepath.Join(dataDir, "sessions")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(origin.String()))
	inner, _ := cookiejar.New(nil)
	jar := &sessionJar{inner: inner, origin: origin, path: filepath.Join(directory, hex.EncodeToString(hash[:])+".json")}
	file, err := os.Open(jar.path)
	if os.IsNotExist(err) {
		return jar, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var saved savedSession
	if json.NewDecoder(io.LimitReader(file, 8192)).Decode(&saved) == nil && saved.Origin == origin.String() && validSession(&saved.Cookie, origin) && saved.Cookie.Expires.After(time.Now()) {
		saved.Cookie.MaxAge = 0
		inner.SetCookies(origin, []*http.Cookie{&saved.Cookie})
	}
	return jar, nil
}

func validSession(cookie *http.Cookie, origin *url.URL) bool {
	return cookie.Name == "spool_session" && len(cookie.Value) <= 4096 && cookie.Secure && cookie.HttpOnly && cookie.Path == "/" && (cookie.Domain == "" || cookie.Domain == origin.Hostname())
}

func (j *sessionJar) Cookies(origin *url.URL) []*http.Cookie { return j.inner.Cookies(origin) }
func (j *sessionJar) SetCookies(origin *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inner.SetCookies(origin, cookies)
	if origin.Scheme+"://"+origin.Host != j.origin.String() {
		return
	}
	for _, cookie := range cookies {
		if cookie.Name != "spool_session" {
			continue
		}
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now())) {
			_ = os.Remove(j.path)
			continue
		}
		if !validSession(cookie, j.origin) {
			continue
		}
		saved := *cookie
		if saved.MaxAge > 0 {
			saved.Expires = time.Now().Add(time.Duration(saved.MaxAge) * time.Second)
		}
		if saved.Expires.IsZero() {
			continue
		}
		saved.MaxAge = 0
		data, err := json.Marshal(savedSession{Origin: j.origin.String(), Cookie: saved})
		if err != nil {
			continue
		}
		// A failed persistence attempt must not invalidate an otherwise successful login.
		file, err := os.CreateTemp(filepath.Dir(j.path), "session-")
		if err != nil {
			continue
		}
		name := file.Name()
		_, err = file.Write(data)
		closeErr := file.Close()
		if err == nil && closeErr == nil {
			_ = os.Rename(name, j.path)
		}
		_ = os.Remove(name)
	}
}
