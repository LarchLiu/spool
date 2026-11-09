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
	id, path, ok := relay.ParseWebPath(r.URL)
	origin, err := url.Parse(relayOrigin(a.settings.RelayURL))
	if !ok || path == "" || a.relayWebPrefix == "" || relay.WebPath(id) != a.relayWebPrefix ||
		err != nil || origin.Host == "" || !strings.EqualFold(r.Host, origin.Host) {
		http.NotFound(w, r)
		return r, false
	}
	r = r.Clone(context.WithValue(r.Context(), webRequestKey{}, webRequest{a.relayWebPrefix, origin.String()}))
	raw := strings.TrimPrefix(r.URL.EscapedPath(), strings.TrimSuffix(a.relayWebPrefix, "/"))
	r.URL.Path = path
	r.URL.RawPath = raw
	return r, true
}
