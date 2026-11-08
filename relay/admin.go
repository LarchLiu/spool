package relay

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
)

// AdminHandler is served on a separate operator listener, never on the public
// relay handler. The CLI defaults to loopback; remote operators use SSH.
func (s *Server) AdminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !authorized(r, s.opts.AdminToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			adminError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Origin") != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
			adminError(w, "invalid administrative request", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/v1/registrations" {
			switch r.Method {
			case http.MethodGet:
				adminJSON(w, http.StatusOK, map[string]any{"registrations": s.registrationStatuses("")})
			case http.MethodPost:
				kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || kind != "application/json" {
					adminError(w, "application/json is required", http.StatusUnsupportedMediaType)
					return
				}
				var input struct {
					Name string `json:"name"`
				}
				r.Body = http.MaxBytesReader(w, r.Body, 4096)
				decoder := json.NewDecoder(r.Body)
				decoder.DisallowUnknownFields()
				if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
					adminError(w, "invalid registration JSON", http.StatusBadRequest)
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
