package workspace

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

type federationFailures struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (f *federationFailures) rejected(hash string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Before(f.until[hash])
}

func (f *federationFailures) reject(hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for key, until := range f.until {
		if !now.Before(until) {
			delete(f.until, key)
		}
	}
	if f.until == nil {
		f.until = make(map[string]time.Time)
	}
	if len(f.until) < 2048 {
		f.until[hash] = now.Add(federationTicketTTL)
	}
}

func (a *App) startFederation() {
	ctx, cancel := context.WithCancel(context.Background())
	a.federationStop = cancel
	a.federationDone = make(chan struct{})
	go func() {
		defer close(a.federationDone)
		sub := a.store.changes.subscribe()
		defer sub.close()
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		a.federationMaintain(ctx)
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.federationMaintain(ctx)
			case <-sub.wake:
				if sub.take()&changeAuth != 0 {
					a.federationMaintain(ctx)
				}
			}
		}
	}()
}

type federationSessionRef struct {
	peer, ref string
}

func (a *App) federationMaintain(ctx context.Context) {
	now := time.Now().UnixMilli()
	offline := now - federationOffline.Milliseconds()
	if result, err := a.store.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash IN (SELECT session_hash FROM federation_sessions WHERE checked_at<=?)`, offline); err == nil {
		if n, err := result.RowsAffected(); err == nil && n != 0 {
			a.store.changes.publish(changeAuth | changePresence)
		}
	}
	_, _ = a.store.db.ExecContext(ctx, `DELETE FROM federation_sources WHERE expires_at<=?;`, now)
	_, _ = a.store.db.ExecContext(ctx, `DELETE FROM federation_revocations WHERE expires_at<=?;`, now)
	// Do not hold a SQLite connection open across network calls. Back off before
	// notifying so one unreachable peer cannot trigger a busy loop.
	refs := a.federationRefs(ctx, `SELECT f.peer_id,f.ref FROM federation_sources f LEFT JOIN auth_sessions a ON a.token_hash=f.session_hash
		JOIN federation_peers p ON p.id=f.peer_id
		WHERE f.notified<=? AND (f.revoked=1 OR a.token_hash IS NULL) AND json_extract(p.config,'$.transport')<>'' ORDER BY f.notified LIMIT 8`, now)
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		_, _ = a.store.db.ExecContext(ctx, `UPDATE federation_sources SET notified=? WHERE ref=?`, time.Now().Add(federationRetry).UnixMilli(), ref.ref)
		peer, err := federationPeer(a.store.db, ref.peer)
		if err != nil || peer.Transport == "" {
			continue
		}
		if _, err = a.federationCall(ctx, peer, federationCancel, ref.ref); err == nil {
			_, _ = a.store.db.ExecContext(ctx, `DELETE FROM federation_sources WHERE ref=?`, ref.ref)
		}
	}
	refs = a.federationRefs(ctx, `SELECT f.peer_id,f.source_ref FROM federation_sessions f JOIN auth_sessions a ON a.token_hash=f.session_hash
		WHERE f.retry_at<=? AND a.expires_at>? GROUP BY f.peer_id,f.source_ref ORDER BY MIN(f.retry_at) LIMIT 8`, now, now)
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		peer, err := federationPeer(a.store.db, ref.peer)
		if err != nil || !peer.Accept {
			continue
		}
		started := time.Now()
		_, _ = a.store.db.ExecContext(ctx, `UPDATE federation_sessions SET retry_at=? WHERE peer_id=? AND source_ref=? AND retry_at<=?`, started.Add(federationRetry).UnixMilli(), ref.peer, ref.ref, now)
		_, err = a.federationCall(ctx, peer, federationCheck, ref.ref)
		if errors.Is(err, errFederationDenied) {
			_ = a.store.federationRevoke(peer, ref.ref)
			continue
		}
		if err != nil {
			continue
		}
		_ = a.store.federationRefresh(peer, ref.ref, started)
	}
}

func (a *App) federationRefs(ctx context.Context, query string, args ...any) []federationSessionRef {
	rows, err := a.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var refs []federationSessionRef
	for rows.Next() {
		var ref federationSessionRef
		if rows.Scan(&ref.peer, &ref.ref) != nil {
			return nil
		}
		refs = append(refs, ref)
	}
	if rows.Err() != nil {
		return nil
	}
	return refs
}

func (s *Store) federationRefresh(peer FederationPeer, ref string, started time.Time) error {
	// Only extend freshness for still-live sessions associated with the same
	// current relationship. A delayed positive reply cannot re-create a row.
	_, err := s.db.Exec(`UPDATE federation_sessions SET checked_at=?,retry_at=?
		WHERE peer_id=? AND source_ref=? AND checked_at<=? AND checked_at>?
		AND EXISTS(SELECT 1 FROM federation_peers WHERE id=? AND generation=?)`,
		started.UnixMilli(), started.Add(federationRecheck).UnixMilli(), peer.ID, ref, started.UnixMilli(), time.Now().Add(-federationOffline).UnixMilli(), peer.ID, peer.generation)
	return err
}

func (s *Store) saveFederationUser(input UserInput, source string) (User, error) {
	if input.Password != "" {
		return User{}, errors.New("external users cannot be assigned a password")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	v := User{ID: input.ID, Disabled: input.Disabled, SourceServerID: source, RemoteDesktop: input.RemoteDesktop, ADBBridge: input.ADBBridge}
	if err := tx.QueryRow(`SELECT u.username,u.nickname,u.created_at,COALESCE(v.style,?),COALESCE(v.seed,'') FROM users u LEFT JOIN user_avatars v ON v.user_id=u.id WHERE u.id=?`, DefaultAvatar().Style, v.ID).Scan(&v.Username, &v.Nickname, &v.CreatedAt, &v.Avatar.Style, &v.Avatar.Seed); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`UPDATE users SET disabled=? WHERE id=?`, v.Disabled, v.ID); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`INSERT INTO user_remote_desktop VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled`, v.ID, v.RemoteDesktop); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`INSERT INTO user_adb_bridge VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled`, v.ID, v.ADBBridge); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`DELETE FROM web_push_subscriptions WHERE user_id=?`, v.ID); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, v.ID); err != nil {
		return User{}, err
	}
	if err = bumpProfileRevision(tx); err != nil {
		return User{}, err
	}
	return v, s.notify(tx.Commit(), changeAuth|changeNavigation|changePresence|changeProfiles|changeADBBridge|changeRemoteDesktop)
}

func (s *Store) federationSource(user string) (string, error) {
	var source string
	err := s.db.QueryRow(`SELECT peer_id FROM federation_users WHERE user_id=?`, user).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return source, err
}
