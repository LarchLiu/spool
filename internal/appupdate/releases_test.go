package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseVersionOrdering(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.0.10", "1.1.0", "2.0.0",
		"999999999999999999999999999.0.0", "1000000000000000000000000000.0.0",
	}
	for i, name := range ordered {
		a, ok := parseReleaseVersion(name)
		if !ok {
			t.Fatal("valid version rejected:", name)
		}
		alias, ok := parseReleaseVersion("v" + name + "+build.01")
		if !ok || compareReleaseVersions(a, alias) != 0 {
			t.Fatal("tag prefix or build metadata changed precedence:", name)
		}
		for j, other := range ordered {
			b, _ := parseReleaseVersion(other)
			got := compareReleaseVersions(a, b)
			if i < j && got >= 0 || i > j && got <= 0 || i == j && got != 0 {
				t.Fatalf("unexpected comparison %s vs %s: %d", name, other, got)
			}
		}
	}
	for _, name := range []string{"", "v1", "v1.0", "1.0.01", "01.0.0", "1.0.0-01", "1.0.0-beta.01", "1.0.0+", "1.0.0+one..two", "vv1.0.0", " 1.0.0", "1.0.0+<html>"} {
		if _, ok := parseReleaseVersion(name); ok {
			t.Fatal("invalid version accepted:", name)
		}
	}
}

func releaseClient(fn func(*http.Request) (int, string)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status, body := fn(req)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})}
}

func TestFetchReleaseNotesPaginatesFiltersAndSorts(t *testing.T) {
	pages := 0
	client := releaseClient(func(req *http.Request) (int, string) {
		pages++
		if req.URL.Host != "api.github.com" || req.URL.Path != "/repos/owner/spool/releases" || req.URL.Query().Get("page") != strconv.Itoa(pages) || req.URL.Query().Get("per_page") != "100" {
			t.Fatal("incorrect request:", req.URL)
		}
		if req.Header.Get("Accept") != "application/vnd.github+json" || req.Header.Get("X-GitHub-Api-Version") == "" || req.Header.Get("User-Agent") == "" {
			t.Fatal("missing GitHub headers")
		}
		if pages == 1 {
			// An old first page must not hide newer versions on later pages.
			releases := make([]map[string]any, 100)
			for i := range releases {
				releases[i] = map[string]any{"tag_name": "v0.1.0"}
			}
			body, _ := json.Marshal(releases)
			return 200, string(body)
		}
		return 200, `[
			{"tag_name":"v1.0.2","name":"Fixes","body":"## Heading\n- **Fix**","published_at":"2026-10-29T00:00:00Z","html_url":"javascript:bad"},
			{"tag_name":"v1.0.10"}, {"tag_name":"1.0.2+build.2","body":"duplicate"},
			{"tag_name":"v1.0.3-beta.2","prerelease":true}, {"tag_name":"v1.0.4-rc.1"},
			{"tag_name":"v1.0.1"}, {"tag_name":"v2.0.0"},
			{"tag_name":"v1.0.5","draft":true}, {"tag_name":"nightly"}
		]`
	})
	notes, err := FetchReleaseNotes(context.Background(), client, "owner/spool", "1.0.1", "1.0.10")
	if err != nil {
		t.Fatal(err)
	}
	versions := []string{}
	for _, note := range notes {
		versions = append(versions, note.Version)
	}
	if pages != 2 || !reflect.DeepEqual(versions, []string{"1.0.10", "1.0.4-rc.1", "1.0.3-beta.2", "1.0.2"}) {
		t.Fatalf("wrong version range or sorting: %v (%d pages)", versions, pages)
	}
	if note := notes[3]; note.Name != "Fixes" || note.Body != "## Heading\n- **Fix**" || note.URL != "https://github.com/owner/spool/releases/tag/v1.0.2" || note.PublishedAt != "2026-10-29T00:00:00Z" || !notes[1].Prerelease || !notes[2].Prerelease {
		t.Fatalf("lost release metadata or trusted unsafe URL: %+v", notes)
	}
}

func TestFetchReleaseNotesRejectsInvalidRangesBeforeNetwork(t *testing.T) {
	client := releaseClient(func(*http.Request) (int, string) { t.Fatal("unexpected request"); return 200, "[]" })
	for _, args := range [][3]string{{"owner/spool", "1.0.0", "1.0.0"}, {"owner/spool", "2.0.0", "1.0.0"}, {"owner/spool", "dev", "1.0.0"}, {"../owner/spool", "1.0.0", "2.0.0"}} {
		if _, err := FetchReleaseNotes(context.Background(), client, args[0], args[1], args[2]); err == nil {
			t.Fatal("invalid range accepted:", args)
		}
	}
}

func TestFetchReleaseNotesFailuresAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		match  string
	}{
		{403, `{}`, "rate limited"}, {429, `{}`, "rate limited"}, {404, `{}`, "HTTP 404"},
		{503, `{}`, "HTTP 503"}, {200, `null`, "invalid release"}, {200, `{}`, "invalid release"},
		{200, `[`, "invalid release"}, {200, strings.Repeat("x", (8<<20)+1), "exceeds"},
	} {
		client := releaseClient(func(*http.Request) (int, string) { return tc.status, tc.body })
		notes, err := FetchReleaseNotes(context.Background(), client, "owner/spool", "1.0.0", "2.0.0")
		if err == nil || !strings.Contains(err.Error(), tc.match) || notes != nil {
			t.Fatalf("unexpected result HTTP %d: %v, %v", tc.status, notes, err)
		}
		var network *NetworkError
		if errors.As(err, &network) != (tc.status == 503) {
			t.Fatalf("incorrect retry classification: %v", err)
		}
	}
	client := releaseClient(func(*http.Request) (int, string) { return 200, `[]` })
	notes, err := FetchReleaseNotes(context.Background(), client, "owner/spool", "1.0.0", "2.0.0")
	if err != nil || notes == nil || len(notes) != 0 {
		t.Fatalf("empty history is not a JSON array: %v, %v", notes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = FetchReleaseNotes(ctx, http.DefaultClient, "owner/spool", "1.0.0", "2.0.0")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("request ignored cancellation:", err)
	}
}

func TestFetchReleaseNotesDoesNotReturnPartialHistory(t *testing.T) {
	requests := 0
	client := releaseClient(func(*http.Request) (int, string) {
		requests++
		if requests > 1 {
			return 500, "unavailable"
		}
		releases := make([]map[string]string, 100)
		for i := range releases {
			releases[i] = map[string]string{"tag_name": "v1.2.0"}
		}
		body, _ := json.Marshal(releases)
		return 200, string(body)
	})
	notes, err := FetchReleaseNotes(context.Background(), client, "owner/spool", "1.0.0", "2.0.0")
	if err == nil || notes != nil || requests != 2 {
		t.Fatalf("partial history appeared complete: %v, %v", notes, err)
	}
}
