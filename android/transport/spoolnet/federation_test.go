package spoolnet

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type federationRoundTrip func(*http.Request) *http.Response

func (f federationRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	res := f(req)
	res.Request = req
	return res, nil
}

func TestFederatedSessionKeepsCredentialsOnTheirOwnTransport(t *testing.T) {
	for _, name := range []string{"exchange", "preserve existing", "changed destination", "new login during exchange", "redirect"} {
		t.Run(name, func(t *testing.T) {
			const aURL, bURL = "https://a.example.test", "https://b.example.test"
			id := strings.Repeat("b", 52)
			ticket := "spool1." + strings.Repeat("a", 52) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
			reply := func(status int, body string) *http.Response {
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
			}
			client := func(url string, transport federationRoundTrip) *Client {
				t.Helper()
				root := t.TempDir()
				c, err := NewDirectClient(filepath.Join(root, "https"), url)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				if err = c.UseServerSession(filepath.Join(root, "session"), url, ""); err != nil {
					t.Fatal(err)
				}
				c.http.Transport = transport
				return c
			}
			issued, exchanged := 0, 0
			a := client(aURL, func(r *http.Request) *http.Response {
				if cookie, _ := r.Cookie("spool_session"); cookie == nil || cookie.Value != "source-fixture" || r.URL.Host != "a.example.test" {
					t.Error("source request lost its origin or session")
				}
				switch r.URL.Path {
				case "/api/me":
					published := bURL
					if name == "changed destination" {
						published = "https://other.example.test"
					}
					return reply(200, fmt.Sprintf(`{"user":{"id":"alice"},"csrf":"source-token","federation_targets":[{"id":%q,"client_url":%q}]}`, id, published))
				case "/api/federation/issue":
					issued++
					if r.Header.Get("X-CSRF-Token") != "source-token" {
						t.Error("source issue request is missing CSRF")
					}
					return reply(200, fmt.Sprintf(`{"ticket":%q}`, ticket))
				default:
					t.Error("unexpected source request", r.URL.Path)
					return reply(404, "")
				}
			})
			b := client(bURL, func(r *http.Request) *http.Response {
				if r.URL.Host != "b.example.test" || r.Header.Get("X-CSRF-Token") != "" {
					t.Error("destination request carried source authorization or used the wrong origin")
				}
				switch r.URL.Path {
				case "/api/version":
					if len(r.Cookies()) != 0 {
						t.Error("identity lookup carried account credentials")
					}
					return reply(200, fmt.Sprintf(`{"server_id":%q}`, id))
				case "/api/me":
					if cookie, _ := r.Cookie("spool_session"); cookie != nil && cookie.Value == "native-fixture" {
						return reply(200, `{"user":{"id":"local"},"csrf":"destination-token"}`)
					}
					return reply(401, "")
				case "/api/federation/exchange":
					exchanged++
					var body map[string]string
					if len(r.Cookies()) != 0 || r.URL.RawQuery != "" || json.NewDecoder(r.Body).Decode(&body) != nil || body["ticket"] != ticket {
						t.Error("exchange did not isolate the body from other credentials")
					}
					if name == "redirect" {
						res := reply(307, "")
						res.Header.Set("Location", "https://other.example.test/api/federation/exchange")
						return res
					}
					res := reply(200, `{"user":{"id":"external"},"csrf":"destination-token"}`)
					cookie := sessionCookie("federated-fixture")
					cookie.Expires = time.Now().Add(time.Hour)
					res.Header.Add("Set-Cookie", cookie.String())
					return res
				default:
					t.Error("unexpected destination request", r.URL.Path)
					return reply(404, "")
				}
			})
			a.http.Jar.SetCookies(a.origin, []*http.Cookie{sessionCookie("source-fixture")})
			if name == "preserve existing" {
				b.http.Jar.SetCookies(b.origin, []*http.Cookie{sessionCookie("native-fixture")})
			}
			result, err := a.PrepareFederatedSession(b, id, "alice", "en")
			switch name {
			case "changed destination":
				if err == nil || issued != 0 || exchanged != 0 {
					t.Fatal("unapproved origin reached the exchange")
				}
				return
			case "redirect":
				if err == nil || issued != 1 || exchanged != 1 || len(b.http.Jar.Cookies(b.origin)) != 0 {
					t.Fatal("redirect accepted or session changed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "new login during exchange" {
				b.http.Jar.SetCookies(b.origin, []*http.Cookie{sessionCookie("native-fixture")})
				if _, err = result.Activate(); err == nil {
					t.Fatal("stale result overwrote a newer login")
				}
				return
			}
			if name == "exchange" && len(b.http.Jar.Cookies(b.origin)) != 0 {
				t.Fatal("unaccepted result installed a session")
			}
			profile, err := result.Activate()
			if err != nil || strings.Contains(profile, ticket) {
				t.Fatal("activation failed or exposed the ticket")
			}
			want := "federated-fixture"
			if name == "preserve existing" {
				want = "native-fixture"
				if issued != 0 || exchanged != 0 {
					t.Fatal("replaced an existing account")
				}
			}
			if cookies := b.http.Jar.Cookies(b.origin); len(cookies) != 1 || cookies[0].Value != want {
				t.Fatal("target session was not adopted")
			}
		})
	}
}

func TestFederatedSessionCannotActivateAfterRetirement(t *testing.T) {
	const endpoint = "https://target.example.test"
	origin, _, _ := parseEndpoint(endpoint)
	jar, err := newServerSessionJar(t.TempDir(), origin, endpoint, "", "https")
	if err != nil {
		t.Fatal(err)
	}
	state := jar.federationState()
	jar.close()
	cookie := sessionCookie("fixture")
	cookie.Expires = time.Now().Add(time.Hour)
	if jar.adoptFederation(state, cookie) {
		t.Fatal("retired route accepted a delayed login")
	}
}
