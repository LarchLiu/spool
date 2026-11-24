package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

const (
	federationUserCheck  = "user-check"
	federationUserRevoke = "user-revoke"
	managedUsersChanged  = changeAuth | changePresence | changeNavigation | changeProfiles | changeRemoteDesktop | changeADBBridge
)

// User access is independent of a source login session. Signing out must not
// disable an account, while withdrawing access also affects signed-out users.
type federationUserAccess struct {
	Subject string `json:"subject"`
	Grant   string `json:"grant"`
}

func disableManagedUsers(tx *sql.Tx, selection string, args ...any) error {
	if err := removeManagedProjectAssignees(tx, selection, args...); err != nil {
		return err
	}
	for _, query := range []string{
		`UPDATE users SET disabled=1 WHERE id IN (`,
		`DELETE FROM project_members WHERE user_id IN (`,
		`DELETE FROM web_push_subscriptions WHERE user_id IN (`,
		`DELETE FROM auth_sessions WHERE user_id IN (`,
	} {
		if _, err := tx.Exec(query+selection+`)`, args...); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT p.id,u.id FROM users u CROSS JOIN federation_peers p
		JOIN federation_trust_grants g ON g.peer_id=p.id AND g.outgoing=1
		WHERE u.id IN (`+selection+`) AND NOT EXISTS(SELECT 1 FROM federation_users WHERE user_id=u.id)
		AND (json_extract(p.config,'$.all_users')=1 OR EXISTS(SELECT 1 FROM json_each(p.config,'$.users') WHERE value=u.id))`, args...)
	if err != nil {
		return err
	}
	var pending [][2]string
	for rows.Next() {
		var item [2]string
		if err = rows.Scan(&item[0], &item[1]); err != nil {
			break
		}
		pending = append(pending, item)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return errors.Join(err, readErr)
	}
	for _, item := range pending {
		if err := queueFederationUserRevocation(tx, item[0], item[1]); err != nil {
			return err
		}
	}
	return bumpProfileRevision(tx)
}

func removeManagedProjectAssignees(tx *sql.Tx, selection string, args ...any) error {
	rows, err := tx.Query(`SELECT id FROM users WHERE id IN (`+selection+`)`, args...)
	if err != nil {
		return err
	}
	removed := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		removed[id] = true
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return errors.Join(err, readErr)
	}
	if len(removed) == 0 {
		return nil
	}
	rows, err = tx.Query(`SELECT project_id,body FROM project_automations`)
	if err != nil {
		return err
	}
	type update struct {
		project string
		value   IssuesAutomation
	}
	var updates []update
	for rows.Next() {
		var project, body string
		var value IssuesAutomation
		if err = rows.Scan(&project, &body); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(body), &value); err != nil {
			break
		}
		before := value.Controllers()
		after := slices.DeleteFunc(value.Controllers(), func(id string) bool { return removed[id] })
		if len(before) == len(after) && !removed[value.UserID] {
			continue
		}
		value.AssigneeIDs = after
		if !slices.Contains(after, value.UserID) {
			value.UserID = ""
			if len(after) > 0 {
				value.UserID = after[0]
			} else {
				value.Enabled = false
			}
		}
		updates = append(updates, update{project, value})
	}
	readErr = rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return errors.Join(err, readErr)
	}
	for _, item := range updates {
		if err := saveIssuesAutomation(tx, item.project, item.value); err != nil {
			return err
		}
	}
	return nil
}

func queueFederationUserRevocations(tx *sql.Tx, current, previous FederationPeer) error {
	rows, err := tx.Query(`SELECT id FROM users WHERE NOT EXISTS(SELECT 1 FROM federation_users WHERE user_id=users.id)`)
	if err != nil {
		return err
	}
	var subjects []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if previous.allows(id) && !current.allows(id) {
			subjects = append(subjects, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(subjects) == 0 {
		return err
	}
	for _, subject := range subjects {
		if err := queueFederationUserRevocation(tx, current.ID, subject); err != nil {
			return err
		}
	}
	return nil
}

func queueFederationUserRevocation(tx *sql.Tx, peer, subject string) error {
	grant, _, err := federationTrustEpoch(tx, peer, true)
	if err != nil && !errors.Is(err, errFederationTrustEpoch) {
		return err
	}
	// Local revocation must not depend on recovering an old approval. Retain an
	// unbound notice until the original evidence can be resolved before sending.
	if err != nil || !federationSecret(grant) {
		grant = ""
	}
	_, err = tx.Exec(`INSERT INTO federation_user_revocations(peer_id,subject,grant_id,nonce) VALUES(?,?,?,?)
		ON CONFLICT(peer_id,subject) DO UPDATE SET grant_id=excluded.grant_id,nonce=excluded.nonce,retry_at=0`, peer, subject, grant, randomToken())
	return err
}

func (s *Store) resolveFederationUserRevocation(ctx context.Context, peer, nonce string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var grant string
	if err := tx.QueryRowContext(ctx, `SELECT grant_id FROM federation_user_revocations WHERE peer_id=? AND nonce=?`, peer, nonce).Scan(&grant); err != nil {
		return "", err
	}
	if grant == "" {
		grant, _, err = federationTrustEpoch(tx, peer, true)
		if err != nil {
			return "", err
		}
		if !federationSecret(grant) {
			return "", errFederationTrustEpoch
		}
		if _, err := tx.ExecContext(ctx, `UPDATE federation_user_revocations SET grant_id=? WHERE peer_id=? AND nonce=?`, grant, peer, nonce); err != nil {
			return "", err
		}
	}
	return grant, tx.Commit()
}

func (s *Store) federationUserAllowed(peer FederationPeer, access federationUserAccess) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	grant, revoked, err := federationTrustEpoch(tx, peer.ID, true)
	if err != nil {
		return false, err
	}
	// A renewal can be approved before its receipt reaches the source. Missing
	// or different approval evidence is not a definitive user eligibility denial.
	if !federationSecret(grant) || grant != access.Grant {
		return false, errFederationTrustEpoch
	}
	if revoked {
		return false, tx.Commit()
	}
	var allowed bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users u
		JOIN federation_peers p ON p.id=? JOIN federation_trust_grants g ON g.peer_id=p.id
		JOIN federation_trust_epochs e ON e.peer_id=p.id AND e.outgoing=1
		WHERE u.id=? AND u.disabled=0 AND g.outgoing=1 AND e.request_id=? AND e.revoked=0
		AND NOT EXISTS(SELECT 1 FROM federation_users WHERE user_id=u.id)
		AND (json_extract(p.config,'$.all_users')=1 OR EXISTS(SELECT 1 FROM json_each(p.config,'$.users') WHERE value=u.id)))`,
		peer.ID, access.Subject, access.Grant).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, tx.Commit()
}

func (s *Store) revokeFederationUser(peer FederationPeer, access federationUserAccess, nonce string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := federationPeer(tx, peer.ID)
	if err != nil {
		return err
	}
	grant, revoked, err := federationTrustEpoch(tx, peer.ID, false)
	if err != nil {
		return err
	}
	// Delayed checks/notifications cannot affect a newer approval or recreate a
	// deleted account. Replayed notifications cannot undo a later manual enable.
	if !current.Accept || current.generation != peer.generation || revoked || grant != access.Grant {
		return nil
	}
	if nonce != "" {
		result, err := tx.Exec(`INSERT OR IGNORE INTO federation_user_revocation_receipts VALUES(?,?)`, peer.ID, nonce)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count == 0 {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO federation_user_access_revocations VALUES(?,?,?)
		ON CONFLICT(peer_id,subject) DO UPDATE SET revoked_at=MAX(revoked_at,excluded.revoked_at)`, peer.ID, access.Subject, time.Now().UnixMilli()); err != nil {
		return err
	}
	if err := disableManagedUsers(tx, `SELECT user_id FROM federation_users WHERE peer_id=? AND subject=?`, peer.ID, access.Subject); err != nil {
		return err
	}
	return s.notify(tx.Commit(), managedUsersChanged)
}

func (a *App) syncFederationUserRevocations(ctx context.Context) {
	rows, err := a.store.db.QueryContext(ctx, `SELECT r.peer_id,r.subject,r.grant_id,r.nonce FROM federation_user_revocations r
		JOIN federation_peers p ON p.id=r.peer_id WHERE r.retry_at<=? AND json_extract(p.config,'$.transport')<>''
		ORDER BY r.retry_at LIMIT 8`, time.Now().UnixMilli())
	if err != nil {
		return
	}
	type notification struct {
		peer, nonce string
		access      federationUserAccess
	}
	var pending []notification
	for rows.Next() {
		var item notification
		if err = rows.Scan(&item.peer, &item.access.Subject, &item.access.Grant, &item.nonce); err != nil {
			break
		}
		pending = append(pending, item)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return
	}
	for _, item := range pending {
		if ctx.Err() != nil {
			return
		}
		peer, err := federationPeer(a.store.db, item.peer)
		if err != nil {
			continue
		}
		_, _ = a.store.db.ExecContext(ctx, `UPDATE federation_user_revocations SET retry_at=? WHERE nonce=?`, time.Now().Add(federationRetry).UnixMilli(), item.nonce)
		if item.access.Grant == "" {
			item.access.Grant, err = a.store.resolveFederationUserRevocation(ctx, item.peer, item.nonce)
			if err != nil {
				continue
			}
		}
		if _, err := a.federationSend(ctx, peer, federationMessage{Action: federationUserRevoke, Value: item.nonce, Access: &item.access}); err == nil {
			_, _ = a.store.db.ExecContext(ctx, `DELETE FROM federation_user_revocations WHERE nonce=?`, item.nonce)
		}
	}
}

func (a *App) checkFederationUsers(ctx context.Context) {
	// Reconcile accounts left enabled by versions that only revoked sessions.
	if err := a.store.disableUntrustedFederationUsers(); err != nil {
		return
	}
	a.store.prepareFederationUserEpochs()
	// Seed accounts created by older versions, including ones with no remaining
	// login sessions. No source session reference is required for eligibility.
	_, err := a.store.db.ExecContext(ctx, `INSERT OR IGNORE INTO federation_user_checks
		SELECT f.user_id,0 FROM federation_users f JOIN users u ON u.id=f.user_id WHERE u.disabled=0`)
	if err != nil {
		return
	}
	rows, err := a.store.db.QueryContext(ctx, `SELECT f.peer_id,f.subject,e.request_id,c.user_id FROM federation_user_checks c
		JOIN federation_users f ON f.user_id=c.user_id JOIN users u ON u.id=f.user_id
		JOIN federation_trust_epochs e ON e.peer_id=f.peer_id AND e.outgoing=0 AND e.revoked=0
		WHERE u.disabled=0 AND c.retry_at<=? ORDER BY c.retry_at LIMIT 8`, time.Now().UnixMilli())
	if err != nil {
		return
	}
	type check struct {
		peer, user string
		access     federationUserAccess
	}
	var pending []check
	for rows.Next() {
		var item check
		if err = rows.Scan(&item.peer, &item.access.Subject, &item.access.Grant, &item.user); err != nil {
			break
		}
		pending = append(pending, item)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return
	}
	for _, item := range pending {
		if ctx.Err() != nil {
			return
		}
		peer, err := federationPeer(a.store.db, item.peer)
		if err != nil || !peer.Accept {
			continue
		}
		_, _ = a.store.db.ExecContext(ctx, `UPDATE federation_user_checks SET retry_at=? WHERE user_id=?`, time.Now().Add(federationRetry).UnixMilli(), item.user)
		_, err = a.federationSend(ctx, peer, federationMessage{Action: federationUserCheck, Value: randomToken(), Access: &item.access})
		if errors.Is(err, errFederationDenied) {
			_ = a.store.revokeFederationUser(peer, item.access, "")
		} else if err == nil {
			_, _ = a.store.db.ExecContext(ctx, `UPDATE federation_user_checks SET retry_at=? WHERE user_id=?`, time.Now().Add(federationRecheck).UnixMilli(), item.user)
		}
	}
}

func (s *Store) prepareFederationUserEpochs() {
	rows, err := s.db.Query(`SELECT DISTINCT f.peer_id FROM federation_users f JOIN users u ON u.id=f.user_id
		LEFT JOIN federation_trust_epochs e ON e.peer_id=f.peer_id AND e.outgoing=0
		WHERE u.disabled=0 AND (e.request_id IS NULL OR e.request_id='')`)
	if err != nil {
		return
	}
	var peers []string
	for rows.Next() {
		var peer string
		if err = rows.Scan(&peer); err != nil {
			break
		}
		peers = append(peers, peer)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return
	}
	for _, peer := range peers {
		tx, err := s.db.Begin()
		if err != nil {
			continue
		}
		if _, _, err := federationTrustEpoch(tx, peer, false); err == nil {
			_ = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
}

func (s *Store) disableUntrustedFederationUsers() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const selection = `SELECT f.user_id FROM federation_users f JOIN federation_peers p ON p.id=f.peer_id
		LEFT JOIN federation_trust_grants g ON g.peer_id=p.id
		WHERE COALESCE(g.incoming,0)=0 OR COALESCE(json_extract(p.config,'$.accept'),0)=0`
	var needed bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE disabled=0 AND id IN (` + selection + `))`).Scan(&needed); err != nil || !needed {
		return err
	}
	if err := disableManagedUsers(tx, selection); err != nil {
		return err
	}
	return s.notify(tx.Commit(), managedUsersChanged)
}
