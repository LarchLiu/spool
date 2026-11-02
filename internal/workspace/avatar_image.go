package workspace

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"spool/internal/avatar"
)

func (a *App) avatarImage(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) > 2048 {
		apiError(w, http.StatusBadRequest, "Invalid avatar options")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		apiError(w, http.StatusBadRequest, "Invalid avatar options")
		return
	}
	for key, values := range query {
		if (key != "style" && key != "seed") || len(values) != 1 {
			apiError(w, http.StatusBadRequest, "Invalid avatar options")
			return
		}
	}
	style := query.Get("style")
	if style == "" {
		style = DefaultAvatar().Style
	}
	image, err := a.avatars.Render(style, query.Get("seed"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, avatar.ErrInvalidOptions) {
			status = http.StatusBadRequest
		}
		apiError(w, status, "Invalid avatar options")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", image.ETag)
	for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == image.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(image.SVG)))
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(image.SVG))
	}
}
