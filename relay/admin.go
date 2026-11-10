package relay

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// AdminHandler is served on a separate operator listener, never on the public
// relay handler. The CLI defaults to loopback; remote operators use SSH.
func (s *Server) AdminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if !validAdminOrigin(r) || r.URL.RawQuery != "" || r.URL.ForceQuery {
			adminError(w, "invalid administrative request", http.StatusBadRequest)
			return
		}
		if serveAdminAsset(w, r) {
			return
		}
		if !authorized(r, s.opts.AdminToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			adminError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/v1/settings" {
			switch r.Method {
			case http.MethodGet:
				adminJSON(w, http.StatusOK, s.settingsStatus())
			case http.MethodPut:
				var input settingsInput
				if !decodeAdminJSON(w, r, &input) {
					return
				}
				if err := s.updateSettings(input); err != nil {
					if errors.Is(err, errInvalidSettings) {
						adminError(w, err.Error(), http.StatusBadRequest)
					} else {
						log.Printf("relay settings operation failed: %v", err)
						adminError(w, "settings storage unavailable", http.StatusInternalServerError)
					}
					return
				}
				adminJSON(w, http.StatusOK, s.settingsStatus())
			default:
				w.Header().Set("Allow", "GET, PUT")
				adminError(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if r.URL.Path == "/v1/proxy-detection" {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				adminError(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if s.settingsStatus().PublicURL == "" {
				adminError(w, "请先通过公网 HTTPS 管理网址访问，或在高级设置中指定公网地址。", http.StatusBadRequest)
				return
			}
			s.StartProxyDetection()
			adminJSON(w, http.StatusAccepted, s.settingsStatus())
			return
		}
		if r.URL.Path == "/v1/registrations" {
			switch r.Method {
			case http.MethodGet:
				adminJSON(w, http.StatusOK, map[string]any{"registrations": s.registrationStatuses("")})
			case http.MethodPost:
				var input struct {
					Name string `json:"name"`
				}
				if !decodeAdminJSON(w, r, &input) {
					return
				}
				registration, token, err := s.CreateRegistration(input.Name)
				if err != nil {
					registrationError(w, err)
					return
				}
				w.Header().Set("Location", "/v1/registrations/"+registration.ID)
				adminJSON(w, http.StatusCreated, struct {
					Registration Registration `json:"registration"`
					Token        string       `json:"token"`
				}{registration, token})
			default:
				w.Header().Set("Allow", "GET, POST")
				adminError(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		id, ok := strings.CutPrefix(r.URL.Path, "/v1/registrations/")
		if !ok || !validToken(id) {
			adminError(w, "not found", http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			list := s.registrationStatuses(id)
			if len(list) == 0 {
				registrationError(w, ErrRegistrationNotFound)
				return
			}
			adminJSON(w, http.StatusOK, list[0])
		case http.MethodDelete:
			if err := s.RevokeRegistration(id); err != nil {
				registrationError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, DELETE")
			adminError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func validAdminOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true // CLI callers use explicit bearer authorization as before.
	}
	if len(origins) != 1 {
		return false
	}
	u, err := url.Parse(origins[0])
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	// HTTPS may terminate at the operator's reverse proxy. No cookies grant
	// access, and forwarded headers never determine administrative authority.
	return u.Scheme == "https" || (u.Scheme == "http" && r.TLS == nil)
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, input any) bool {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		adminError(w, "application/json is required", http.StatusUnsupportedMediaType)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(input) != nil || decoder.Decode(new(any)) != io.EOF {
		adminError(w, "invalid administrative JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func registrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRegistration):
		adminError(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrRegistrationExists):
		adminError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrRegistrationNotFound):
		adminError(w, err.Error(), http.StatusNotFound)
	default:
		log.Printf("relay registration operation failed: %v", err)
		adminError(w, "registration storage unavailable", http.StatusInternalServerError)
	}
}

func adminError(w http.ResponseWriter, message string, status int) {
	adminJSON(w, status, map[string]string{"error": message})
}
func adminJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
