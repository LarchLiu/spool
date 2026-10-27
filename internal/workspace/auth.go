package workspace

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	chatmodel "spool/internal/model"
)

const authCookie = "spool_session"

type authIdentity struct {
	User      User
	CSRF      string
	TokenHash string
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Store) authenticate(token string) (authIdentity, error) {
	var v authIdentity
	v.TokenHash = tokenHash(token)
	err := s.db.QueryRow(`SELECT u.id,u.username,u.nickname,u.disabled,u.created_at,a.csrf,COALESCE(av.style,?),COALESCE(av.seed,'') FROM auth_sessions a JOIN users u ON u.id=a.user_id LEFT JOIN user_avatars av ON av.user_id=u.id WHERE a.token_hash=? AND a.expires_at>? AND u.disabled=0`, DefaultAvatar().Style, v.TokenHash, time.Now().UnixMilli()).Scan(&v.User.ID, &v.User.Username, &v.User.Nickname, &v.User.Disabled, &v.User.CreatedAt, &v.CSRF, &v.User.Avatar.Style, &v.User.Avatar.Seed)
	return v, err
}
func (s *Store) login(username, password string) (string, authIdentity, error) {
	var id string
	var hash string
	var disabled bool
	err := s.db.QueryRow(`SELECT id,password_hash,disabled FROM users WHERE username=?`, strings.TrimSpace(username)).Scan(&id, &hash, &disabled)
	if err != nil {
		hash = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"
	}
	compare := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil || compare != nil || disabled {
		return "", authIdentity{}, fmt.Errorf("用户名或密码不正确")
	}
	token := randomToken()
	csrf := randomToken()
	tx, err := s.db.Begin()
	if err != nil {
		return "", authIdentity{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE expires_at<=?`, time.Now().UnixMilli()); err != nil {
		return "", authIdentity{}, err
	}
	if _, err = tx.Exec(`INSERT INTO auth_sessions VALUES(?,?,?,?)`, tokenHash(token), id, csrf, time.Now().Add(7*24*time.Hour).UnixMilli()); err != nil {
		return "", authIdentity{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", authIdentity{}, err
	}
	identity, err := s.authenticate(token)
	return token, identity, err
}
func (a *App) identity(r *http.Request) (authIdentity, error) {
	c, err := r.Cookie(authCookie)
	if err != nil || len(c.Value) > 128 {
		return authIdentity{}, fmt.Errorf("请先登录")
	}
	return a.store.authenticate(c.Value)
}
func (a *App) secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(a.settings.PublicURL, "https://")
}
func (a *App) sameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if origin == scheme+"://"+r.Host {
		return true
	}
	return a.settings.PublicURL != "" && origin == a.settings.PublicURL
}
func (a *App) authorizeMutation(w http.ResponseWriter, r *http.Request, id authIdentity) bool {
	provided := r.Header.Get("X-CSRF-Token")
	if !a.sameOrigin(r) || len(provided) != len(id.CSRF) || subtle.ConstantTimeCompare([]byte(provided), []byte(id.CSRF)) != 1 {
		apiError(w, 403, "请求来源或 CSRF 校验失败")
		return false
	}
	return true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		apiError(w, 415, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, chatmodel.MaxMessageBytes+16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		apiError(w, 400, "无效的 JSON 请求")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		apiError(w, 400, "只允许一个 JSON 对象")
		return false
	}
	return true
}

type loginAttempt struct {
	Count int
	Until time.Time
}
type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]loginAttempt
}

func (l *loginLimiter) allow(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]loginAttempt{}
	}
	now := time.Now()
	for k, v := range l.buckets {
		if !now.Before(v.Until) {
			delete(l.buckets, k)
		}
	}
	v := l.buckets[host]
	if v.Count == 0 {
		v.Until = now.Add(10 * time.Minute)
	}
	if v.Count >= 20 || len(l.buckets) > 4096 {
		return false
	}
	v.Count++
	l.buckets[host] = v
	return true
}
