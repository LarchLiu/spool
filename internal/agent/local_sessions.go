package agent

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	chatmodel "spool/internal/model"
)

// LocalSession is provider-owned metadata. Transcript bodies stay in the native
// history store and are read only when a chat is opened.
type LocalSession struct {
	ID, Path, Directory, Title string
	CustomTitle, FirstPrompt   string
	GitBranch, Tag             string
	FileSize                   int64
	Generation                 string
	Backend                    chatmodel.Backend
	CreatedAt, UpdatedAt       int64
	TitleAuthoritative         bool
}

type ListSessionsOptions struct {
	Directory           string
	Limit, Offset       int
	IncludeWorktrees    *bool // Claude only: nil means true, matching its SDK
	IncludeProgrammatic *bool // Claude only: nil means true, matching its SDK
	// Claude only: keep sessions without any derivable title (Title is empty).
	// The SDK omits them; Spool lists them under the provider label.
	IncludeUntitled bool
}

// SessionLister lets each provider own discovery, filtering and pagination.
// An empty Directory lists all projects; Limit <= 0 means no limit. Codex uses
// an exact cwd filter; Claude optionally includes the directory's git worktrees.
type SessionLister interface {
	ListSessions(context.Context, ListSessionsOptions) ([]LocalSession, error)
}

type SessionListerFunc func(context.Context, ListSessionsOptions) ([]LocalSession, error)

func (f SessionListerFunc) ListSessions(ctx context.Context, options ListSessionsOptions) ([]LocalSession, error) {
	return f(ctx, options)
}

type localSessionFile struct {
	info    os.FileInfo
	session LocalSession
}

const (
	// Providers list newest first and Spool shows a small per-project window,
	// so discovery never pages through or samples an unbounded history.
	localSessionDiscoveryLimit = 2000
	// A failing provider (missing, hung, or outdated CLI) is not retried on
	// every refresh, and its last snapshot is withdrawn once it is this stale.
	localSessionRetryInterval = time.Minute
	localSessionStaleAfter    = 5 * time.Minute
)

type localSessionProvider struct {
	sessions          []LocalSession
	failedAt, retryAt time.Time
}

// LocalSessionIndex only tracks generations of metadata returned by providers.
// Callers serialize discovery. A failed provider retains its last successful
// snapshot for a bounded time, so a transient CLI failure cannot withdraw its
// chats, while a permanently broken one cannot show deleted chats forever.
type LocalSessionIndex struct {
	files     map[string]localSessionFile
	providers map[chatmodel.Backend]*localSessionProvider
	now       func() time.Time
}

// Failing reports providers whose latest listing failed, for diagnostics.
func (index *LocalSessionIndex) Failing() []chatmodel.Backend {
	var failing []chatmodel.Backend
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		if provider := index.providers[backend]; provider != nil && !provider.failedAt.IsZero() {
			failing = append(failing, backend)
		}
	}
	return failing
}

func (index *LocalSessionIndex) Discover(ctx context.Context, sources map[chatmodel.Backend]SessionLister) ([]LocalSession, error) {
	if index.providers == nil {
		index.providers = make(map[chatmodel.Backend]*localSessionProvider)
	}
	now := time.Now()
	if index.now != nil {
		now = index.now()
	}
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		source := sources[backend]
		if source == nil {
			// A provider that is no longer listed (disabled) owns no snapshot.
			delete(index.providers, backend)
			continue
		}
		provider := index.providers[backend]
		if provider == nil {
			provider = &localSessionProvider{}
			index.providers[backend] = provider
		}
		if !now.Before(provider.retryAt) {
			sessions, err := source.ListSessions(ctx, ListSessionsOptions{Limit: localSessionDiscoveryLimit, IncludeUntitled: true})
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == nil {
				*provider = localSessionProvider{sessions: sessions}
				continue
			}
			if provider.failedAt.IsZero() {
				provider.failedAt = now
			}
			provider.retryAt = now.Add(localSessionRetryInterval)
		}
		if !provider.failedAt.IsZero() && now.Sub(provider.failedAt) >= localSessionStaleAfter {
			provider.sessions = nil
		}
	}
	files := make(map[string]localSessionFile)
	var sessions []LocalSession
	for backend, provider := range index.providers {
		for _, v := range provider.sessions {
			if !chatmodel.ValidID(v.ID) || !filepath.IsAbs(v.Directory) || (v.Path != "" && !filepath.IsAbs(v.Path)) {
				continue
			}
			if v.Title == "" {
				v.Title, v.TitleAuthoritative = backend.Label(), false
			}
			v.Backend = backend
			key := string(backend) + ":" + v.ID
			var info os.FileInfo
			if v.Path != "" {
				info, _ = os.Stat(v.Path)
			}
			cached, ok := index.files[key]
			sameFile := info == nil && cached.info == nil || info != nil && cached.info != nil && info.Size() == cached.info.Size() && info.ModTime().Equal(cached.info.ModTime()) && os.SameFile(info, cached.info)
			if ok && sameFile && v.Path == cached.session.Path && v.UpdatedAt == cached.session.UpdatedAt {
				v.Generation = cached.session.Generation
			} else {
				v.Generation = chatmodel.NewID()
			}
			files[key] = localSessionFile{info: info, session: v}
			sessions = append(sessions, v)
		}
	}
	index.files = files
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt != sessions[j].UpdatedAt {
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		}
		if sessions[i].Backend != sessions[j].Backend {
			return sessions[i].Backend < sessions[j].Backend
		}
		return sessions[i].ID < sessions[j].ID
	})
	return sessions, nil
}
