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

// Only a locally enabled, identity-matching route can mount the application
// below a relay path. Forwarded headers cannot enable this route.
func (a *App) relayWebRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	a.configMu.RLock()
	prefix, relayURL := a.relayWebPrefix, a.settings.RelayURL
	a.configMu.RUnlock()
	id, path, ok := relay.ParseWebPath(r.URL)
	origin, err := url.Parse(relayOrigin(relayURL))
	if !ok || path == "" || prefix == "" || relay.WebPath(id) != prefix ||
		err != nil || origin.Host == "" || !strings.EqualFold(r.Host, origin.Host) {
		http.NotFound(w, r)
		return r, false
	}
	r = r.Clone(context.WithValue(r.Context(), webRequestKey{}, webRequest{prefix, origin.String()}))
	raw := strings.TrimPrefix(r.URL.EscapedPath(), strings.TrimSuffix(prefix, "/"))
	r.URL.Path = path
	r.URL.RawPath = raw
	return r, true
}
