package workspace

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Linked Git worktrees can live outside the project's directory. Match their
// Git common directory, not their name or an untrusted transcript project label.
func gitCommonDirectory(path string) string {
	for dir := path; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		git := filepath.Join(dir, ".git")
		info, err := os.Stat(git)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			body, err := os.ReadFile(git)
			if err != nil || !strings.HasPrefix(string(body), "gitdir: ") {
				return ""
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
		}
		return canonicalDirectory(git)
	}
	return ""
}

func (s *Store) syncLocalSessions(force bool) error {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if !force && time.Since(s.localScanAt) < localSessionInterval {
		return nil
	}
	projects, err := s.Projects()
	if err != nil {
		return err
	}
	shared := false
	for _, p := range projects {
		shared = shared || (!p.Archived && p.SharedChats && p.SharedLocalChats)
	}
	if !shared {
		return nil
	}
	local, err := s.localIndex.Discover()
	if err != nil {
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
	counts, paths, common := map[string]int{}, map[string]string{}, map[string]string{}
	for _, p := range projects {
		paths[p.ID] = canonicalDirectory(p.Path)
		// A configured subdirectory does not grant access to every sibling in
		// its parent repository. Only repository/worktree roots opt into this.
		if _, err := os.Stat(filepath.Join(p.Path, ".git")); err == nil {
			common[p.ID] = gitCommonDirectory(p.Path)
		}
	}
	changed := false
	for _, native := range local {
		key := string(native.Backend) + ":" + native.ID
		if known[key] {
			continue
		}
		directory := canonicalDirectory(native.Directory)
		if directory == "" {
			continue
		}
		var match Project
		// Prefer the most specific configured project before checking opt-in;
		// a parent project must not publish a private nested project's history.
		for _, p := range projects {
			if path := paths[p.ID]; path != "" && (directory == path || strings.HasPrefix(directory, path+string(filepath.Separator))) && len(path) > len(paths[match.ID]) {
				match = p
			}
		}
		if match.ID == "" {
			git := gitCommonDirectory(directory)
			for _, p := range projects {
				if git != "" && git == common[p.ID] {
					if match.ID != "" { // Ambiguous repository mapping fails closed.
						match = Project{}
						break
					}
					match = p
				}
			}
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
		result, err := tx.Exec(`INSERT INTO sessions(id,project_id,title,backend,native_session_id,native_session_path,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,native_session_path=excluded.native_session_path,updated_at=MAX(sessions.updated_at+1,excluded.updated_at) WHERE sessions.status<>'running' AND (sessions.title<>excluded.title OR sessions.native_session_path<>excluded.native_session_path OR ?)`, id, match.ID, native.Title, native.Backend, native.ID, native.Path, chatmodel.StatusCompleted, native.CreatedAt, native.UpdatedAt, fileChanged)
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
		ticker := time.NewTicker(localSessionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.shutdown:
				return
			case <-ticker.C:
				// Do not expose provider paths in remote error responses/logs.
				_ = a.store.syncLocalSessions(false)
			}
		}
	}()
}
