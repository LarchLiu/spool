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
		var workers sync.WaitGroup
		workers.Go(func() { a.runFederationTrustRevocations(ctx) })
		defer workers.Wait()
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
	a.syncFederationUserRevocations(ctx)
	a.checkFederationUsers(ctx)
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
		answer, err := a.federationCall(ctx, peer, federationCheck, ref.ref)
		if errors.Is(err, errFederationDenied) {
			_ = a.store.federationRevoke(peer, ref.ref)
			continue
		}
		if err != nil {
			continue
		}
		_ = a.store.federationRefresh(peer, ref.ref, started, answer.Subject)
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

func (s *Store) federationRefresh(peer FederationPeer, ref string, started time.Time, person *federationSubject) error {
	if person != nil && (!person.valid() || person.SourceRef != ref) {
		return errFederationDenied
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Only extend freshness for still-live sessions associated with the same
	// current relationship. A delayed positive reply cannot re-create a row or
	// update a profile after the session has been revoked.
	now := time.Now()
	result, err := tx.Exec(`UPDATE federation_sessions SET checked_at=?,retry_at=?
		WHERE peer_id=? AND source_ref=? AND checked_at<=? AND checked_at>?
		AND EXISTS(SELECT 1 FROM auth_sessions a JOIN users u ON u.id=a.user_id
			WHERE a.token_hash=session_hash AND a.expires_at>? AND u.disabled=0)
		AND EXISTS(SELECT 1 FROM federation_peers p JOIN federation_trust_grants g ON g.peer_id=p.id
			WHERE p.id=? AND p.generation=? AND g.incoming=1 AND json_extract(p.config,'$.accept')=1)`,
		started.UnixMilli(), started.Add(federationRecheck).UnixMilli(), peer.ID, ref, started.UnixMilli(), now.Add(-federationOffline).UnixMilli(), now.UnixMilli(), peer.ID, peer.generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	// Older peers only confirm eligibility. They can still refresh freshness
	// without overwriting the profile received during login.
	if person == nil {
		return tx.Commit()
	}
	var profile UserProfile
	var subject string
	err = tx.QueryRow(`SELECT u.id,f.subject,u.nickname,COALESCE(v.style,?),COALESCE(v.seed,'')
		FROM federation_sessions fs JOIN auth_sessions a ON a.token_hash=fs.session_hash
		JOIN federation_users f ON f.user_id=a.user_id AND f.peer_id=fs.peer_id
		JOIN users u ON u.id=f.user_id LEFT JOIN user_avatars v ON v.user_id=u.id
		WHERE fs.peer_id=? AND fs.source_ref=? AND fs.checked_at=? LIMIT 1`,
		DefaultAvatar().Style, peer.ID, ref, started.UnixMilli()).Scan(&profile.ID, &subject, &profile.Nickname, &profile.Avatar.Style, &profile.Avatar.Seed)
	if err != nil {
		return err
	}
	if subject != person.ID {
		return errFederationDenied
	}
	var changes changeKind
	if profile.Nickname != person.Nickname || profile.Avatar != person.Avatar {
		if _, err = tx.Exec(`UPDATE users SET nickname=? WHERE id=?`, person.Nickname, profile.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO user_avatars VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET style=excluded.style,seed=excluded.seed`, profile.ID, person.Avatar.Style, person.Avatar.Seed); err != nil {
			return err
		}
		if err = bumpProfileRevision(tx); err != nil {
			return err
		}
		changes = changeProfiles | changePresence
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if changes != 0 {
		s.changes.publish(changes)
	}
	return nil
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
	v := User{ID: input.ID, SourceServerID: source}
	if err := tx.QueryRow(`SELECT u.username,u.nickname,u.created_at,u.disabled,COALESCE(v.style,?),COALESCE(v.seed,''),COALESCE(rd.enabled,0),COALESCE(ab.enabled,0),COALESCE(json_extract(p.config,'$.name'),''),f.subject
		FROM users u JOIN federation_users f ON f.user_id=u.id JOIN federation_peers p ON p.id=f.peer_id LEFT JOIN user_avatars v ON v.user_id=u.id LEFT JOIN user_remote_desktop rd ON rd.user_id=u.id LEFT JOIN user_adb_bridge ab ON ab.user_id=u.id WHERE u.id=? AND NOT EXISTS(SELECT 1 FROM deleted_users WHERE user_id=u.id)`, DefaultAvatar().Style, v.ID).
		Scan(&v.Username, &v.Nickname, &v.CreatedAt, &v.Disabled, &v.Avatar.Style, &v.Avatar.Seed, &v.RemoteDesktop, &v.ADBBridge, &v.SourceServerName, &v.SourceUserID); err != nil {
		return User{}, err
	}
	// Older management clients send the current profile, while permissions-only
	// updates can omit it. A rejected or unchanged update must not sign users out.
	if (input.Username != "" && input.Username != v.Username) || (input.Nickname != "" && input.Nickname != v.Nickname) {
		return User{}, errors.New("External account usernames and display names cannot be edited locally.")
	}
	if input.Disabled == v.Disabled && input.RemoteDesktop == v.RemoteDesktop && input.ADBBridge == v.ADBBridge {
		return v, nil
	}
	v.Disabled, v.RemoteDesktop, v.ADBBridge = input.Disabled, input.RemoteDesktop, input.ADBBridge
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
	if v.Disabled {
		if err := disableManagedUsers(tx, `SELECT id FROM users WHERE id=?`, v.ID); err != nil {
			return User{}, err
		}
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
