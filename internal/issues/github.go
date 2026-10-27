package issues

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func ValidRepository(repo string) bool {
	return repositoryPattern.MatchString(repo) && !strings.Contains(repo, "..")
}

type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
	PRs         []PR            `json:"associated_prs,omitempty"`
	Comments    []Comment       `json:"comments_data,omitempty"`
}

type Comment struct {
	Body string `json:"body"`
	URL  string `json:"html_url"`
}

type PR struct {
	URL      string  `json:"url"`
	State    string  `json:"state"`
	MergedAt *string `json:"mergedAt"`
	Draft    bool    `json:"isDraft"`
	Head     string  `json:"headRefName"`
	Base     string  `json:"baseRefName"`
}

func (p PR) Open() bool { return p.State == "OPEN" && p.MergedAt == nil }

type GitHub struct {
	Run                   Command
	Directory, Repository string
}

// ConnectedEvent covers PRs linked through GitHub's Development sidebar that
// need not mention the issue in a body/comment (and so have no REST cross-ref).
func (g GitHub) connectedPRs(ctx context.Context, number int) ([]string, error) {
	query := `query($owner:String!,$name:String!,$number:Int!,$endCursor:String) {
  repository(owner:$owner,name:$name) { issue(number:$number) {
    timelineItems(first:100,after:$endCursor,itemTypes:[CONNECTED_EVENT,DISCONNECTED_EVENT]) {
      nodes { __typename
        ... on ConnectedEvent { subject { __typename ... on PullRequest { url } } source { __typename ... on PullRequest { url } } }
        ... on DisconnectedEvent { subject { __typename ... on PullRequest { url } } source { __typename ... on PullRequest { url } } }
      }
      pageInfo { hasNextPage endCursor }
    }
  } }
}`
	parts := strings.Split(g.Repository, "/")
	body, err := g.Run(ctx, g.Directory, "gh", "api", "graphql", "--paginate", "--slurp", "-f", "query="+query, "-f", "owner="+parts[0], "-f", "name="+parts[1], "-F", fmt.Sprintf("number=%d", number))
	if err != nil {
		return nil, RemoteError(err)
	}
	type connection struct {
		Nodes []struct {
			Kind    string `json:"__typename"`
			Subject struct {
				Kind string `json:"__typename"`
				URL  string `json:"url"`
			} `json:"subject"`
			Source struct {
				Kind string `json:"__typename"`
				URL  string `json:"url"`
			} `json:"source"`
		} `json:"nodes"`
		PageInfo *struct {
			HasNext bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	}
	var pages []struct {
		Errors []json.RawMessage `json:"errors"`
		Data   struct {
			Repository *struct {
				Issue *struct {
					Timeline *connection `json:"timelineItems"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &pages); err != nil || len(pages) == 0 {
		return nil, fmt.Errorf("invalid GitHub linked-PR JSON")
	}
	links := map[string]bool{}
	for i, page := range pages {
		if len(page.Errors) > 0 || page.Data.Repository == nil || page.Data.Repository.Issue == nil || page.Data.Repository.Issue.Timeline == nil {
			return nil, fmt.Errorf("incomplete GitHub linked-PR response")
		}
		timeline := page.Data.Repository.Issue.Timeline
		if timeline.Nodes == nil || timeline.PageInfo == nil {
			return nil, fmt.Errorf("incomplete GitHub linked-PR timeline")
		}
		if i == len(pages)-1 && timeline.PageInfo.HasNext {
			return nil, fmt.Errorf("GitHub linked-PR pagination incomplete")
		}
		for _, node := range timeline.Nodes {
			if node.Kind != "ConnectedEvent" && node.Kind != "DisconnectedEvent" {
				return nil, fmt.Errorf("invalid GitHub connection event")
			}
			for _, subject := range []struct {
				Kind string `json:"__typename"`
				URL  string `json:"url"`
			}{node.Subject, node.Source} {
				if subject.Kind != "PullRequest" {
					continue
				}
				if !ValidPRURL(subject.URL) {
					return nil, fmt.Errorf("invalid linked PR URL")
				}
				if node.Kind == "ConnectedEvent" {
					links[subject.URL] = true
				} else if node.Kind == "DisconnectedEvent" {
					delete(links, subject.URL)
				}
			}
		}
	}
	out := []string{}
	for link := range links {
		out = append(out, link)
	}
	sort.Strings(out)
	return out, nil
}

func pages[T any](ctx context.Context, g GitHub, endpoint string) ([]T, error) {
	body, err := g.Run(ctx, g.Directory, "gh", "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, RemoteError(err)
	}
	var pages [][]T
	if err = json.Unmarshal(body, &pages); err != nil || len(pages) == 0 {
		return nil, fmt.Errorf("GitHub returned invalid/incomplete JSON for %s", endpoint)
	}
	out := []T{}
	for _, page := range pages {
		if page == nil {
			return nil, fmt.Errorf("GitHub returned a null page for %s", endpoint)
		}
		out = append(out, page...)
	}
	return out, nil
}

func (g GitHub) PR(ctx context.Context, link string) (PR, error) {
	var p PR
	if !ValidPRURL(link) {
		return p, fmt.Errorf("invalid GitHub PR URL: %s", link)
	}
	body, err := g.Run(ctx, g.Directory, "gh", "pr", "view", link, "--json", "url,state,mergedAt,isDraft,headRefName,baseRefName")
	if err != nil {
		return p, RemoteError(err)
	}
	if err = json.Unmarshal(body, &p); err != nil || p.URL != link || (p.State != "OPEN" && p.State != "CLOSED" && p.State != "MERGED") {
		return p, fmt.Errorf("GitHub returned invalid PR data for %s", link)
	}
	return p, nil
}

func ValidPRURL(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || !ValidRepository(parts[0]+"/"+parts[1]) {
		return false
	}
	n, err := strconv.Atoi(parts[3])
	return err == nil && n > 0
}

// Snapshot reads every issue and its actual timeline references, then verifies
// every referenced PR. A failed page or PR lookup invalidates the entire snapshot.
func (g GitHub) Snapshot(ctx context.Context, ledger *Ledger) ([]Issue, error) {
	all, err := pages[Issue](ctx, g, "repos/"+g.Repository+"/issues?state=all&sort=created&direction=asc&per_page=100")
	if err != nil {
		return nil, err
	}
	var out []Issue
	cache := map[string]PR{}
	seen := map[int]bool{}
	for _, issue := range all {
		if len(issue.PullRequest) != 0 && string(issue.PullRequest) != "null" {
			continue
		}
		if issue.Number <= 0 || (issue.State != "open" && issue.State != "closed") || issue.URL == "" {
			return nil, fmt.Errorf("invalid GitHub issue data")
		}
		if seen[issue.Number] {
			return nil, fmt.Errorf("GitHub returned duplicate issue #%d; retry a consistent snapshot", issue.Number)
		}
		seen[issue.Number] = true
		type event struct {
			Event  string `json:"event"`
			Source struct {
				Issue struct {
					URL         string          `json:"html_url"`
					PullRequest json.RawMessage `json:"pull_request"`
				} `json:"issue"`
			} `json:"source"`
		}
		events, e := pages[event](ctx, g, fmt.Sprintf("repos/%s/issues/%d/timeline?per_page=100", g.Repository, issue.Number))
		if e != nil {
			return nil, e
		}
		links := map[string]bool{}
		connected, e := g.connectedPRs(ctx, issue.Number)
		if e != nil {
			return nil, e
		}
		for _, link := range connected {
			links[link] = true
		}
		for _, ev := range events {
			if ev.Event == "cross-referenced" && len(ev.Source.Issue.PullRequest) > 0 && string(ev.Source.Issue.PullRequest) != "null" {
				if !ValidPRURL(ev.Source.Issue.URL) {
					return nil, fmt.Errorf("invalid GitHub cross-reference PR URL")
				}
				links[ev.Source.Issue.URL] = true
			}
		}
		if record := ledger.Records[issue.Number]; record != nil {
			for _, link := range record.PRs {
				links[link] = true
			}
			// Also discover a push/PR that succeeded immediately before a crash,
			// even if its URL never made it into the local ledger.
			if record.Branch != "" {
				type pull struct {
					URL  string `json:"html_url"`
					Head struct {
						Ref string `json:"ref"`
					} `json:"head"`
				}
				pulls, e := pages[pull](ctx, g, "repos/"+g.Repository+"/pulls?state=all&per_page=100&head="+url.QueryEscape(strings.Split(g.Repository, "/")[0]+":"+record.Branch))
				if e != nil {
					return nil, e
				}
				for _, p := range pulls {
					if p.Head.Ref == record.Branch {
						links[p.URL] = true
					}
				}
			}
		}
		keys := make([]string, 0, len(links))
		for link := range links {
			keys = append(keys, link)
		}
		sort.Strings(keys)
		for _, link := range keys {
			p, exists := cache[link]
			if !exists {
				p, err = g.PR(ctx, link)
				if err != nil {
					return nil, err
				}
				cache[link] = p
			}
			issue.PRs = append(issue.PRs, p)
		}
		out = append(out, issue)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

func (g GitHub) Comments(ctx context.Context, number int) ([]Comment, error) {
	return pages[Comment](ctx, g, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", g.Repository, number))
}

func Categories(issue Issue) []string {
	out := []string{}
	for _, name := range []string{"web", "server", "app"} {
		for _, label := range issue.Labels {
			if label.Name == name {
				out = append(out, name)
				break
			}
		}
	}
	return out
}
