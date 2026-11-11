package workspace

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"spool/relay"
)

type webRequestKey struct{}
type webRequest struct{ prefix, origin string }

func requestWebPath(r *http.Request) string {
	if web, ok := r.Context().Value(webRequestKey{}).(webRequest); ok {
		return web.prefix
	}
	return "/"
}

func requestAuthCookie(r *http.Request) string {
	if _, ok := r.Context().Value(webRequestKey{}).(webRequest); ok {
		return "__Host-spool_session"
	}
	return authCookie
}

func (a *App) configureRelayWebOrigin(address string) {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	a.relayWebOrigin = strings.TrimSuffix(address, "/")
}

// Only a locally enabled, identity-matching route can mount the application
// below a relay path. Forwarded headers cannot enable this route.
func (a *App) relayWebRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	a.configMu.RLock()
	prefix, relayURL, assigned := a.relayWebPrefix, a.settings.RelayURL, a.relayWebOrigin
	a.configMu.RUnlock()
	id, path, ok := relay.ParseWebPath(r.URL)
	if assigned == "" {
		assigned, _ = relay.WebURL(relayOrigin(relayURL), id)
		assigned = strings.TrimSuffix(assigned, "/")
	}
	origin, err := url.Parse(assigned)
	if !ok || path == "" || prefix == "" || relay.WebPath(id) != prefix ||
		err != nil || origin.Host == "" || !relay.ValidateWebURL(assigned+"/", id) || !strings.EqualFold(r.Host, origin.Host) {
		http.NotFound(w, r)
		return r, false
	}
	r = r.Clone(context.WithValue(r.Context(), webRequestKey{}, webRequest{"/", origin.String()}))
	raw := strings.TrimPrefix(r.URL.EscapedPath(), strings.TrimSuffix(prefix, "/"))
	r.URL.Path = path
	r.URL.RawPath = raw
	return r, true
}
