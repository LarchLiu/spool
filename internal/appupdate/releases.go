package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type ReleaseNotes struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
}

type ReleaseHistory struct {
	CurrentVersion string         `json:"current_version"`
	TargetVersion  string         `json:"target_version"`
	Releases       []ReleaseNotes `json:"releases"`
}

func Repository() string {
	if releaseRepository != "" {
		return releaseRepository
	}
	return "LarchLiu/spool"
}

type releaseVersion struct {
	name string
	core []string
	pre  []string
}

var buildMetadataPattern = regexp.MustCompile(`^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$`)

func parseReleaseVersion(value string) (releaseVersion, bool) {
	value = strings.TrimPrefix(value, "v")
	plain, build, hasBuild := strings.Cut(value, "+")
	if !versionPattern.MatchString(plain) || hasBuild && !buildMetadataPattern.MatchString(build) {
		return releaseVersion{}, false
	}
	core, pre, _ := strings.Cut(plain, "-")
	v := releaseVersion{name: value, core: strings.Split(core, ".")}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
		for _, part := range v.pre {
			if numericIdentifier(part) && len(part) > 1 && part[0] == '0' {
				return releaseVersion{}, false
			}
		}
	}
	return v, true
}

func numericIdentifier(value string) bool {
	return value != "" && strings.Trim(value, "0123456789") == ""
}

func compareIdentifier(a, b string) int {
	na, nb := numericIdentifier(a), numericIdentifier(b)
	if na && nb && len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if na != nb {
		if na {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func compareReleaseVersions(a, b releaseVersion) int {
	for i := range a.core {
		if result := compareIdentifier(a.core[i], b.core[i]); result != 0 {
			return result
		}
	}
	if len(a.pre) == 0 || len(b.pre) == 0 {
		if len(a.pre) == len(b.pre) {
			return 0
		}
		if len(a.pre) == 0 {
			return 1
		}
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if result := compareIdentifier(a.pre[i], b.pre[i]); result != 0 {
			return result
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

// Release descriptions are untrusted display content, never update metadata.
// Read every page: GitHub orders releases by creation, not semantic version.
func FetchReleaseNotes(ctx context.Context, client *http.Client, repository, current, target string) ([]ReleaseNotes, error) {
	from, fromOK := parseReleaseVersion(current)
	to, toOK := parseReleaseVersion(target)
	if !repositoryPattern.MatchString(repository) || !fromOK || !toOK || compareReleaseVersions(from, to) >= 0 {
		return nil, errors.New("Invalid release version range")
	}
	type githubRelease struct {
		Tag         string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
	}
	notes := []ReleaseNotes{}
	seen := map[string]bool{}
	totalBytes := 0
	for page := 1; page <= 100; page++ {
		address := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", repository, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "Spool")
		response, err := client.Do(req)
		if err != nil {
			return nil, networkFailure(err)
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			response.Body.Close()
			return nil, errors.New("GitHub release requests are rate limited. Try again later.")
		}
		response, err = updateResponse(response, nil)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("GitHub releases returned HTTP %d", response.StatusCode)
		}
		body, err := readLimited(response.Body, 8<<20)
		if err != nil {
			return nil, networkFailure(err)
		}
		totalBytes += len(body)
		if totalBytes > 32<<20 {
			return nil, errors.New("Release history exceeds the size limit")
		}
		var releases []githubRelease
		if err := json.Unmarshal(body, &releases); err != nil || strings.TrimSpace(string(body)) == "null" {
			return nil, errors.New("GitHub returned invalid release information")
		}
		for _, release := range releases {
			version, ok := parseReleaseVersion(release.Tag)
			if release.Draft || !ok || compareReleaseVersions(version, from) <= 0 || compareReleaseVersions(version, to) > 0 {
				continue
			}
			identity, _, _ := strings.Cut(version.name, "+")
			if seen[identity] {
				continue
			}
			seen[identity] = true
			notes = append(notes, ReleaseNotes{
				Version: version.name, Name: release.Name, Body: release.Body,
				URL:         "https://github.com/" + repository + "/releases/tag/" + url.PathEscape(release.Tag),
				PublishedAt: release.PublishedAt, Prerelease: release.Prerelease || len(version.pre) != 0,
			})
		}
		if len(releases) < 100 {
			slices.SortStableFunc(notes, func(a, b ReleaseNotes) int {
				av, _ := parseReleaseVersion(a.Version)
				bv, _ := parseReleaseVersion(b.Version)
				return compareReleaseVersions(bv, av)
			})
			return notes, nil
		}
	}
	return nil, errors.New("Release history exceeds the page limit")
}
