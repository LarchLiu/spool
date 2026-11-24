package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const federationTrustRevoke = "trust-revoke"

// Outgoing is relative to the sender. The receiver changes the opposite flag.
type federationTrustRevocation struct {
	Outgoing bool   `json:"outgoing"`
	Grant    string `json:"grant"`
}

var errFederationTrustEpoch = errors.New("Cannot identify the current server trust approval")

func federationTrustDirections(tx *sql.Tx, p FederationPeer) (incoming, outgoing bool, err error) {
	err = tx.QueryRow(`SELECT incoming,outgoing FROM federation_trust_grants WHERE peer_id=?`, p.ID).Scan(&incoming, &outgoing)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func federationTrustEpoch(tx *sql.Tx, peer string, outgoing bool) (request string, revoked bool, err error) {
	err = tx.QueryRow(`SELECT request_id,revoked FROM federation_trust_epochs WHERE peer_id=? AND outgoing=?`, peer, outgoing).Scan(&request, &revoked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) || err == nil && request != "" {
		return
	}
	// The previous release saved approvals without epochs. Resolve their identity
	// before sending or acknowledging a revocation, even after the request expired.
	var candidates int
	err = tx.QueryRow(`SELECT min(id),count(*) FROM federation_trust_requests WHERE peer_id=? AND outgoing=? AND decision='approved'
		HAVING count(*)>0`, peer, outgoing).Scan(&request, &candidates)
	if errors.Is(err, sql.ErrNoRows) {
		return "", revoked, nil
	}
	if err != nil {
		return
	}
	if candidates != 1 {
		// Request expiry orders creation, not approval. Without an epoch there
		// is no safe way to choose between multiple completed approvals.
		return "", revoked, errFederationTrustEpoch
	}
	_, err = tx.Exec(`INSERT INTO federation_trust_epochs VALUES(?,?,?,?)
		ON CONFLICT(peer_id,outgoing) DO UPDATE SET request_id=excluded.request_id WHERE request_id=''`, peer, outgoing, request, revoked)
	return
}

func recordFederationTrustGrant(tx *sql.Tx, peer string, outgoing bool, request string) error {
	var previous string
	var revoked bool
	err := tx.QueryRow(`SELECT request_id,revoked FROM federation_trust_epochs WHERE peer_id=? AND outgoing=?`, peer, outgoing).Scan(&previous, &revoked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if revoked && previous == request {
		return errFederationDenied
	}
	if outgoing {
		// Unbound notices belong to an unresolved earlier approval. An explicit
		// approval must retire them before they can bind to the new relationship.
		if _, err := tx.Exec(`DELETE FROM federation_user_revocations WHERE peer_id=? AND grant_id=''`, peer); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO federation_trust_epochs VALUES(?,?,?,0)
		ON CONFLICT(peer_id,outgoing) DO UPDATE SET request_id=excluded.request_id,revoked=0`, peer, outgoing, request)
	return err
}

func (s *Store) revokeFederationTrust(target string, outgoing bool) error {
	local, err := s.federationKey()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := federationPeer(tx, target)
	if err != nil {
		return err
	}
	in, out, err := federationTrustDirections(tx, p)
	if err != nil {
		return err
	}
	if outgoing && !out || !outgoing && !in {
		return nil
	}
	grant, _, err := federationTrustEpoch(tx, target, outgoing)
	if err != nil {
		return err
	}
	if !federationSecret(grant) {
		return errFederationTrustEpoch
	}
	// Also cancel an unconsumed renewal: the target may already have approved it.
	if outgoing {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO federation_trust_notifications
			SELECT peer_id,1,id,?,0 FROM federation_trust_requests
			WHERE peer_id=? AND outgoing=1 AND decision=''`, randomToken(), target); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO federation_trust_notifications VALUES(?,?,?,?,0)
		ON CONFLICT(peer_id,outgoing,request_id) DO UPDATE SET nonce=excluded.nonce,retry_at=0`,
		target, outgoing, grant, randomToken()); err != nil {
		return err
	}
	if err := applyFederationTrustRevocation(tx, p, outgoing); err != nil {
		return err
	}
	if err := recordFederationTrustRevocation(tx, target, outgoing, grant, local.ID()); err != nil {
		return err
	}
	return s.notify(tx.Commit(), managedUsersChanged|changeFederationTargets)
}

// The initiator is the local identity for a management action, or the verified
// sender of a signed notification. Retries must not overwrite the first record.
func recordFederationTrustRevocation(tx *sql.Tx, peer string, outgoing bool, grant, initiator string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO federation_trust_revocation_log VALUES(?,?,?,?,?)`,
		peer, outgoing, grant, initiator, time.Now().UnixMilli())
	return err
}

func applyFederationTrustRevocation(tx *sql.Tx, p FederationPeer, outgoing bool) error {
	in, out, err := federationTrustDirections(tx, p)
	if err != nil {
		return err
	}
	if outgoing {
		out, p.AllUsers, p.Users = false, false, nil
		if _, err := tx.Exec(`DELETE FROM federation_trust_requests WHERE peer_id=? AND outgoing=1 AND decision=''`, p.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE federation_sources SET revoked=1,notified=0 WHERE peer_id=? AND revoked=0`, p.ID); err != nil {
			return err
		}
	} else {
		in, p.Accept, p.Projects = false, false, nil
		if err := disableManagedUsers(tx, `SELECT user_id FROM federation_users WHERE peer_id=?`, p.ID); err != nil {
			return err
		}
	}
	p.generation = randomToken()
	if err := updateFederationPeerConfig(tx, p); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO federation_trust_grants VALUES(?,?,?,1)
		ON CONFLICT(peer_id) DO UPDATE SET incoming=excluded.incoming,outgoing=excluded.outgoing,managed=1`, p.ID, in, out); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO federation_trust_epochs VALUES(?,?,?,1)
		ON CONFLICT(peer_id,outgoing) DO UPDATE SET revoked=1`, p.ID, outgoing, "")
	return err
}

func (s *Store) receiveFederationTrustRevocation(peer FederationPeer, notice federationTrustRevocation) error {
	if !federationSecret(notice.Grant) {
		return errFederationTrustRequest
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	outgoing := !notice.Outgoing
	grant, revoked, err := federationTrustEpoch(tx, peer.ID, outgoing)
	if err != nil {
		return err
	}
	// A target can revoke before the source consumes the receipt, including when
	// a peer already exists for the independent reverse direction.
	var pending bool
	if outgoing {
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_trust_requests WHERE id=? AND peer_id=? AND outgoing=1 AND decision='')`, notice.Grant, peer.ID).Scan(&pending); err != nil {
			return err
		}
	}
	if !pending && grant == "" {
		in, out, err := federationTrustDirections(tx, peer)
		if err != nil {
			return err
		}
		if outgoing && out || !outgoing && in {
			return errFederationTrustEpoch
		}
	}
	if !pending && (grant != notice.Grant || revoked) {
		return nil // Duplicate or an old notification after a new approval.
	}
	current, lookup := federationPeer(tx, peer.ID)
	if lookup != nil && !errors.Is(lookup, sql.ErrNoRows) {
		return lookup
	}
	if errors.Is(lookup, sql.ErrNoRows) {
		if !pending {
			return nil
		}
		// Retain the pinned identity without granting access, so an acknowledgement
		// lost in transit can be retried after the pending application is deleted.
		current = peer
		current.Accept, current.AllUsers, current.Users, current.Projects = false, false, nil, nil
		config, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO federation_peers VALUES(?,?,?)`, peer.ID, config, randomToken()); err != nil {
			return err
		}
	}
	if err := applyFederationTrustRevocation(tx, current, outgoing); err != nil {
		return err
	}
	if pending {
		if _, err := tx.Exec(`INSERT INTO federation_trust_epochs VALUES(?,?,?,1)
			ON CONFLICT(peer_id,outgoing) DO UPDATE SET request_id=excluded.request_id,revoked=1`, peer.ID, outgoing, notice.Grant); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM federation_trust_requests WHERE id=?`, notice.Grant); err != nil {
			return err
		}
	}
	if err := recordFederationTrustRevocation(tx, peer.ID, outgoing, notice.Grant, peer.ID); err != nil {
		return err
	}
	return s.notify(tx.Commit(), managedUsersChanged|changeFederationTargets)
}

// Pending outbound applications already pin the target's public identity.
func (s *Store) pendingFederationTrustPeer(id string) (p FederationPeer, err error) {
	var config string
	err = s.db.QueryRow(`SELECT t.config FROM federation_trust_routes t JOIN federation_trust_requests r ON r.id=t.request_id
		WHERE r.peer_id=? AND r.outgoing=1 AND r.decision='' AND r.expires_at>? ORDER BY r.expires_at DESC LIMIT 1`, id, time.Now().Unix()).Scan(&config)
	if err == nil {
		err = json.Unmarshal([]byte(config), &p)
	}
	return
}

func (a *App) syncFederationTrustRevocations(ctx context.Context, target string) {
	now := time.Now().UnixMilli()
	rows, err := a.store.db.QueryContext(ctx, `SELECT n.peer_id,n.outgoing,n.request_id,n.nonce FROM federation_trust_notifications n
		JOIN federation_peers p ON p.id=n.peer_id WHERE n.retry_at<=? AND (?='' OR n.peer_id=?)
		AND json_extract(p.config,'$.transport')<>'' ORDER BY n.retry_at LIMIT 8`, now, target, target)
	if err != nil {
		return
	}
	type pending struct {
		peer, nonce string
		notice      federationTrustRevocation
	}
	var notices []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.peer, &item.notice.Outgoing, &item.notice.Grant, &item.nonce); err != nil {
			rows.Close()
			return
		}
		notices = append(notices, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, item := range notices {
		if ctx.Err() != nil {
			return
		}
		claim, err := a.store.db.ExecContext(ctx, `UPDATE federation_trust_notifications SET retry_at=? WHERE nonce=? AND retry_at<=?`, time.Now().Add(5*time.Second).UnixMilli(), item.nonce, now)
		if err != nil {
			continue
		}
		if n, err := claim.RowsAffected(); err != nil || n == 0 {
			continue
		}
		peer, err := federationPeer(a.store.db, item.peer)
		if err != nil {
			continue
		}
		if _, err := a.federationSend(ctx, peer, federationMessage{Action: federationTrustRevoke, Value: item.nonce, Trust: &item.notice}); err == nil {
			if _, err := a.store.db.ExecContext(ctx, `DELETE FROM federation_trust_notifications WHERE nonce=?`, item.nonce); err == nil {
				a.store.changes.publish(changeFederationTargets)
			}
		}
	}
}

func (a *App) runFederationTrustRevocations(ctx context.Context) {
	sub := a.store.changes.subscribe()
	defer sub.close()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		a.syncFederationTrustRevocations(ctx, "")
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-sub.wake:
			sub.take()
		}
	}
}

// A running server is woken by the transaction; management-only CLI calls also
// attempt delivery before their temporary client shuts down. Failed sends stay queued.
func (s *Service) RevokeFederationTrust(ctx context.Context, target, direction string) error {
	if direction == "" {
		peers, err := s.Store.FederationTrustSnapshot()
		if err != nil {
			return err
		}
		for _, p := range peers.Peers {
			if p.ID == target && !p.Incoming && p.Outgoing {
				direction = "outgoing"
			}
		}
	}
	var err error
	switch direction {
	case "", "incoming":
		err = s.Store.RevokeFederationTrust(target)
	case "outgoing":
		err = s.Store.WithdrawFederationTrust(target)
	default:
		return errors.New("Choose an incoming or outgoing trust direction")
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	running := s.app != nil && s.app.federationDone != nil
	s.mu.Unlock()
	if running {
		// The committed change wakes the running worker. UI actions return as soon
		// as local revocation is saved, regardless of peer availability.
		return nil
	}
	peer, err := federationPeer(s.Store.db, target)
	if err != nil || peer.Transport == "" {
		return err
	}
	_ = s.federationTrustClient(ctx, peer.Transport, func(ctx context.Context, a *App) error {
		a.syncFederationTrustRevocations(ctx, target)
		return nil
	})
	return nil
}
