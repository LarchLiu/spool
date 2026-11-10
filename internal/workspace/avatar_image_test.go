package workspace

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"spool/internal/avatar"
)

func TestAvatarImageIsAuthenticatedPrivateAndCacheable(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "avatar-reader")
	app := NewApp(store, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, app, user.Username)
	path := "/api/avatars.svg?style=clay&seed=Alice%20%F0%9F%A4%96"
	if got := apiRequest(app, "GET", path, "", nil, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous image request: %d", got.Code)
	}
	got := apiRequest(app, "GET", path, "", cookie, "")
	want, err := avatar.NewRenderer().Render("clay", "Alice 🤖")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != http.StatusOK || got.Body.String() != want.SVG {
		t.Fatalf("wrong image: %d %s", got.Code, got.Body.String())
	}
	if got.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" ||
		got.Header().Get("Cache-Control") != "private, max-age=86400" ||
		got.Header().Get("ETag") != want.ETag || got.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(got.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("wrong image headers: %v", got.Header())
	}
	request := localRequest(http.MethodGet, path, nil)
	request.AddCookie(cookie)
	request.Header.Set("If-None-Match", `"unrelated", W/`+want.ETag)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("conditional response: %d %s", response.Code, response.Body.String())
	}
	head := apiRequest(app, "HEAD", path, "", cookie, "")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != got.Header().Get("Content-Length") {
		t.Fatalf("HEAD response: %d %v", head.Code, head.Header())
	}
}

func TestAvatarImageOnlyAcceptsBundledStylesAndBoundedSeeds(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "avatar-validation")
	app := NewApp(store, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, app, user.Username)
	for _, query := range []string{"style=https://example.com/remote.svg", "style=../clay", "style=initials",
		"style=clay&style=thumbs", "seed=x&seed=y", "svg=%3Cscript%3E", "size=99999999", "seed=%00", "seed=%ff", "seed=%zz",
		"seed=" + strings.Repeat("x", 513), "seed=" + strings.Repeat("x", 2049)} {
		response := apiRequest(app, "GET", "/api/avatars.svg?"+query, "", cookie, "")
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("accepted options %q: %d", query, response.Code)
		}
	}
	for _, style := range []string{"clay", "pixel-art", "croodles", "lorelei", "notionists", "toon-head", "voxel-art", "thumbs"} {
		response := apiRequest(app, "GET", "/api/avatars.svg?"+url.Values{"style": {style}, "seed": {`<script onload="bad()">`}}.Encode(), "", cookie, "")
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script") || strings.Contains(response.Body.String(), "onload=") {
			t.Fatalf("unsafe seed output for %s: %d", style, response.Code)
		}
	}
}
