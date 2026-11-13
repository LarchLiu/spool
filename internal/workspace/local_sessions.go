package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

const localSessionLimit = 20
const localSessionInterval = 10 * time.Second

func canonicalDirectory(path string) string {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(path)
}

func gitWorkspace(path string) (root, common string) {
	if path == "" {
		return "", ""
	}
	for dir := path; ; dir = filepath.Dir(dir) {
		git := filepath.Join(dir, ".git")
		info, err := os.Stat(git)
		if err != nil {
			if !os.IsNotExist(err) {
				return dir, ""
			}
			if dir == filepath.Dir(dir) {
				break
			}
			continue
		}
		if !info.IsDir() {
			body, err := os.ReadFile(git)
			if err != nil || !strings.HasPrefix(string(body), "gitdir: ") {
				return dir, ""
			}
			git = strings.TrimSpace(strings.TrimPrefix(string(body), "gitdir: "))
			if !filepath.IsAbs(git) {
				git = filepath.Join(dir, git)
			}
		}
		if body, err := os.ReadFile(filepath.Join(git, "commondir")); err == nil {
			common := strings.TrimSpace(string(body))
			if !filepath.IsAbs(common) {
				common = filepath.Join(git, common)
			}
			git = common
		} else if !os.IsNotExist(err) {
			return dir, ""
		}
		return dir, canonicalDirectory(git)
	}
	return "", ""
}

func directoryWithin(directory, parent string) bool {
	if parent == "" {
		return false
	}
	relative, err := filepath.Rel(parent, directory)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type localSessionProject struct {
	project                      Project
	path, root, common, relative string
}

func localSessionProjects(projects []Project) []localSessionProject {
	out := make([]localSessionProject, 0, len(projects))
	for _, p := range projects {
		path := canonicalDirectory(p.Path)
		root, common := gitWorkspace(path)
		relative := ""
		if root != "" {
			relative, _ = filepath.Rel(root, path)
		}
		out = append(out, localSessionProject{p, path, root, common, relative})
	}
	return out
}

// Project boundaries apply in every linked checkout, including private nested
// directories. A broad physical ancestor cannot override a repository mapping.
func matchLocalSessionProject(directory string, projects []localSessionProject) Project {
	root, common := gitWorkspace(directory)
	if root != "" && common == "" {
		return Project{}
	}
	var match Project
	bestPath := ""
	bestDirect, publish, ambiguous := false, false, false
	consider := func(p Project, path string, direct, allowed bool) {
		if !directoryWithin(directory, path) {
			return
		}
		if len(path) > len(bestPath) || (len(path) == len(bestPath) && direct && !bestDirect) {
			match, bestPath, bestDirect, publish, ambiguous = p, path, direct, allowed, false
		} else if len(path) == len(bestPath) && direct == bestDirect && p.ID != match.ID {
			ambiguous = true
		}
	}
	for _, p := range projects {
		consider(p.project, p.path, true, true)
		if common != "" && common == p.common {
			path := filepath.Join(root, p.relative)
			direct := path == p.path
			// Subdirectories constrain mirrored roots but do not themselves
			// opt into publishing other checkouts of their parent repository.
			consider(p.project, path, direct, direct || p.path == p.root)
		}
	}
	if ambiguous || !publish {
		return Project{}
	}
	return match
}

func (s *Store) syncLocalSessions(force bool) error {
	return s.syncLocalSessionsContext(s.localContext, force)
}

// localSessionTitle is the single stored form of a native title: one line of
// at most 200 bytes. It is idempotent, and both discovery and opened history
// apply it, so neither path rewrites the other's title.
func localSessionTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if len(title) <= 200 {
		return title
	}
	end := 200 - len("…")
	for end > 0 && !utf8.RuneStart(title[end]) {
		end--
	}
	return strings.TrimRight(title[:end], " ") + "…"
}

func sharesLocalSessions(projects []Project) bool {
	for _, p := range projects {
		if !p.Archived && p.SharedChats && p.SharedLocalChats {
			return true
		}
	}
	return false
}

// A disabled provider is neither executed nor listed.
func (s *Store) localSessionSources() (map[chatmodel.Backend]agent.SessionLister, error) {
	if s.localSources != nil {
		return s.localSources, nil
	}
	providers, err := s.Agents()
	if err != nil {
		return nil, err
	}
	sources := map[chatmodel.Backend]agent.SessionLister{}
	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		switch provider.Backend {
		case chatmodel.BackendCodex:
			sources[provider.Backend] = agent.CodexSessionLister{Binary: provider.Executable}
		case chatmodel.BackendClaude:
			sources[provider.Backend] = s.localClaude
		}
	}
	return sources, nil
}

func (s *Store) syncLocalSessionsContext(ctx context.Context, force bool) error {
	// Discovery may start a provider CLI and take seconds. Only one runs at a
	// time, and a page request never queues behind another caller's discovery:
	// it serves the last projection and a navigation event follows any change.
	if force {
		s.localScanMu.Lock()
	} else if !s.localScanMu.TryLock() {
		return nil
	}
	defer s.localScanMu.Unlock()
	s.localMu.Lock()
	fresh := !force && time.Since(s.localScanAt) < localSessionInterval
	s.localMu.Unlock()
	if fresh {
		return nil
	}
	projects, err := s.Projects()
	if err != nil || !sharesLocalSessions(projects) {
		return err
	}
	sources, err := s.localSessionSources()
	if err != nil {
		return err
	}
	local, err := s.localIndex.Discover(ctx, sources)
	if err != nil {
		return err
	}
	var failing []string
	for _, backend := range s.localIndex.Failing() {
		failing = append(failing, string(backend))
	}
	if state := strings.Join(failing, ","); state != s.localFailing {
		// Report each outage and recovery once. Provider errors can contain
		// native paths, so only the provider is named.
		if s.localFailing = state; state == "" {
			log.Printf("event=local_session_discovery_recovered")
		} else {
			log.Printf("event=local_session_discovery_failed backends=%s", state)
		}
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	// Sharing may have changed while providers were listed.
	if projects, err = s.Projects(); err != nil || !sharesLocalSessions(projects) {
		return err
	}
	// Serialize with native-session adoption/conversion so discovery cannot
	// race a provider ID being bound to a newly-created Spool chat.
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	known := map[string]bool{}
	rows, err := tx.Query(`SELECT backend,native_session_id FROM spool_native_sessions UNION SELECT backend,native_session_id FROM sessions WHERE NOT EXISTS(SELECT 1 FROM local_sessions WHERE session_id=sessions.id)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var backend, id string
		if err = rows.Scan(&backend, &id); err != nil {
			break
		}
		known[backend+":"+id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	previous := map[string]bool{}
	rows, err = tx.Query(`SELECT session_id FROM local_sessions WHERE visible=1`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		previous[id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE local_sessions SET visible=0`); err != nil {
		return err
	}
	counts, matches := map[string]int{}, map[string]Project{}
	mappings := localSessionProjects(projects)
	changed := false
	for _, native := range local {
		native.Title = localSessionTitle(native.Title)
		if native.CreatedAt == 0 {
			native.CreatedAt = native.UpdatedAt
		}
		key := string(native.Backend) + ":" + native.ID
		if known[key] {
			continue
		}
		directory := canonicalDirectory(native.Directory)
		if directory == "" {
			continue
		}
		match, matched := matches[directory]
		if !matched {
			match = matchLocalSessionProject(directory, mappings)
			matches[directory] = match
		}
		if match.ID == "" || match.Archived || !match.SharedChats || !match.SharedLocalChats || counts[match.ID] >= localSessionLimit {
			continue
		}
		known[key] = true
		counts[match.ID]++
		sum := sha256.Sum256([]byte(match.ID + ":" + key))
		id := fmt.Sprintf("local-%x", sum[:16])
		var generation string
		if err = tx.QueryRow(`SELECT generation FROM local_session_files WHERE session_id=?`, id).Scan(&generation); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// A changed file must advance the stream revision even when its mtime
		// stays in the same millisecond or moves backwards after replacement.
		fileChanged := generation != native.Generation
		// A prefix/tail sample may miss a newer title in the middle. Never
		// replace a confirmed title with that provisional metadata.
		result, err := tx.Exec(`INSERT INTO sessions(id,project_id,title,backend,native_session_id,native_session_path,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=CASE WHEN ? THEN excluded.title ELSE sessions.title END,native_session_path=excluded.native_session_path,updated_at=MAX(sessions.updated_at+1,excluded.updated_at) WHERE sessions.status<>'running' AND ((? AND sessions.title<>excluded.title) OR sessions.native_session_path<>excluded.native_session_path OR ?)`, id, match.ID, native.Title, native.Backend, native.ID, native.Path, chatmodel.StatusCompleted, native.CreatedAt, native.UpdatedAt, native.TitleAuthoritative, native.TitleAuthoritative, fileChanged)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n > 0 {
			changed = true
		}
		if !previous[id] {
			changed = true
		}
		delete(previous, id)
		if _, err = tx.Exec(`INSERT INTO local_sessions(session_id,visible,directory) VALUES(?,1,?) ON CONFLICT(session_id) DO UPDATE SET visible=1,directory=excluded.directory`, id, directory); err != nil {
			return err
		}
		// Leave the generation pending while a Spool turn is running so its
		// final native writes are picked up after that turn completes.
		if _, err = tx.Exec(`INSERT INTO local_session_files(session_id,generation) SELECT id,? FROM sessions WHERE id=? AND status<>'running' ON CONFLICT(session_id) DO UPDATE SET generation=excluded.generation`, native.Generation, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.localScanAt = time.Now()
	if changed || len(previous) > 0 {
		s.changes.publish(changeNavigation)
	}
	return nil
}

func (s *Store) sessionDirectory(chat chatmodel.Session) (string, error) {
	if chat.LocalSession {
		var directory string
		err := s.db.QueryRow(`SELECT directory FROM local_sessions WHERE session_id=?`, chat.ID).Scan(&directory)
		return directory, err
	}
	var directory string
	err := s.db.QueryRow(`SELECT directory FROM session_workspaces WHERE session_id=?`, chat.ID).Scan(&directory)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return directory, err
	}
	if chat.ProjectID == "" {
		return "", nil
	}
	p, err := s.Project(chat.ProjectID)
	return p.Path, err
}

func (a *App) startLocalSessionSync() {
	a.streams.Add(1)
	go func() {
		defer a.streams.Done()
		ctx, cancel := context.WithCancel(a.store.localContext)
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			select {
			case <-a.shutdown:
				cancel()
			case <-ctx.Done():
			}
		}()
		defer func() { cancel(); <-stopped }()
		ticker := time.NewTicker(localSessionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.shutdown:
				return
			case <-ticker.C:
				// Do not expose provider paths in remote error responses/logs.
				_ = a.store.syncLocalSessionsContext(ctx, false)
			}
		}
	}()
}
