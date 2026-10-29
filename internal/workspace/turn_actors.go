package workspace

import (
	"database/sql"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

func migrateTurnActorSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`PRAGMA table_info(turn_actors)`)
	if err != nil {
		return err
	}
	hasKind := false
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			break
		}
		hasKind = hasKind || name == "actor_kind"
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if !hasKind {
		// CREATE TABLE IF NOT EXISTS cannot upgrade an existing sidecar table.
		// Older rows are human actors; retain their IDs, bindings and audit data.
		if _, err = tx.Exec(`ALTER TABLE turn_actors ADD COLUMN actor_kind TEXT NOT NULL DEFAULT 'user'`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Actor metadata is kept separately from provider-owned transcript bodies.
// The offset bridges a crash before the provider announces its native turn ID.
func (s *Store) attributeTurns(chat *chatmodel.Session) error {
	rows, err := s.db.Query(`SELECT turn_id,CASE WHEN actor_kind='bot' THEN 'spool-bot' ELSE COALESCE(user_id,'') END,native_session_id,native_turn_id,source_offset FROM turn_actors WHERE session_id=? ORDER BY started_at,turn_id`, chat.ID)
	if err != nil {
		return err
	}
	type actor struct {
		id, user, session, native string
		offset                    int64
	}
	var actors []actor
	for rows.Next() {
		var v actor
		if err = rows.Scan(&v.id, &v.user, &v.session, &v.native, &v.offset); err != nil {
			break
		}
		actors = append(actors, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	byID, byNative := map[string]actor{}, map[string]actor{}
	for _, v := range actors {
		byID[v.id] = v
		if v.native != "" && (v.session == "" || v.session == chat.NativeSessionID) {
			byNative[v.native] = v
		}
	}
	// Prefer an exact provider identity. Never apply an unresolved offset to
	// every later turn, or a failed request to a different provider session.
	for _, v := range actors {
		if v.native != "" || v.offset < 0 || (v.session != "" && v.session != chat.NativeSessionID) {
			continue
		}
		for _, turn := range chat.Turns {
			if turn.NativeTurnID != "" && turn.SourceOffset >= v.offset {
				if bound, ok := byNative[turn.NativeTurnID]; !ok || bound.native == "" {
					byNative[turn.NativeTurnID] = v
				}
				break
			}
		}
	}
	actions, err := s.db.Query(`SELECT turn_id,id,user_id,kind,approval_id,created_at FROM turn_actions WHERE session_id=? ORDER BY created_at,id`, chat.ID)
	if err != nil {
		return err
	}
	defer actions.Close()
	byTurn := map[string][]chatmodel.TurnAction{}
	for actions.Next() {
		var id string
		var v chatmodel.TurnAction
		if err := actions.Scan(&id, &v.ID, &v.UserID, &v.Kind, &v.ApprovalID, &v.CreatedAt); err != nil {
			return err
		}
		byTurn[id] = append(byTurn[id], v)
	}
	if err := actions.Err(); err != nil {
		return err
	}
	for i := range chat.Turns {
		turn := &chat.Turns[i]
		v, ok := byID[turn.ID]
		if !ok {
			v, ok = byNative[turn.NativeTurnID]
		}
		turn.UserID = chat.UserID
		if ok {
			turn.UserID = v.user
			turn.Actions = append([]chatmodel.TurnAction(nil), byTurn[v.id]...)
		}
	}
	return nil
}

func insertTurnAction(tx *sql.Tx, chat, turn, user, kind, approval string) (chatmodel.TurnAction, error) {
	v := chatmodel.TurnAction{ID: chatmodel.NewID(), UserID: user, Kind: kind, ApprovalID: approval, CreatedAt: time.Now().UnixMilli()}
	_, err := tx.Exec(`INSERT INTO turn_actions(session_id,turn_id,id,kind,user_id,approval_id,created_at) VALUES(?,?,?,?,?,?,?)`, chat, turn, v.ID, kind, user, approval, v.CreatedAt)
	return v, err
}

func (s *Store) recordTurnAction(chat, turn, user, kind, approval string) (chatmodel.TurnAction, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return chatmodel.TurnAction{}, err
	}
	defer tx.Rollback()
	v, err := insertTurnAction(tx, chat, turn, user, kind, approval)
	if err != nil {
		return v, err
	}
	if _, err = tx.Exec(`UPDATE sessions SET updated_at=MAX(updated_at+1,?) WHERE id=?`, v.CreatedAt, chat); err != nil {
		return v, err
	}
	return v, tx.Commit()
}

// Batch only the already-authorized chat IDs; listing chats must not decode
// native histories or expose users from unrelated projects. Include the owner
// for legacy turns, plus everyone who actually sent, stopped or approved a turn.
func populateParticipants(db chatMetadataReader, chats []chatmodel.Session) error {
	for start := 0; start < len(chats); start += 200 {
		batch := chats[start:min(start+200, len(chats))]
		args, positions := make([]any, 0, len(batch)), map[string]int{}
		for i := range batch {
			args = append(args, batch[i].ID)
			positions[batch[i].ID] = i
			batch[i].Participants = nil
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := db.Query(`WITH selected AS (SELECT id,user_id FROM sessions WHERE id IN (`+placeholders+`)), operators AS (
			SELECT id AS session_id,user_id FROM selected s WHERE COALESCE((SELECT source_offset FROM turn_actors WHERE session_id=s.id ORDER BY started_at,turn_id LIMIT 1),-1)<>0
			UNION SELECT a.session_id,a.user_id FROM turn_actors a JOIN selected s ON s.id=a.session_id
			UNION SELECT a.session_id,a.user_id FROM turn_actions a JOIN selected s ON s.id=a.session_id
		), people AS (
			SELECT o.session_id,u.id,u.nickname,COALESCE(a.style,'clay') AS style,COALESCE(a.seed,'') AS seed,0 AS bot FROM operators o JOIN users u ON u.id=o.user_id LEFT JOIN user_avatars a ON a.user_id=u.id
			UNION ALL SELECT DISTINCT a.session_id,'spool-bot','Spool Bot','clay','',1 FROM turn_actors a JOIN selected s ON s.id=a.session_id WHERE a.actor_kind='bot'
		) SELECT session_id,id,nickname,style,seed,bot FROM people ORDER BY session_id,bot DESC,id`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var v chatmodel.ChatParticipant
			if err = rows.Scan(&id, &v.ID, &v.Nickname, &v.Avatar.Style, &v.Avatar.Seed, &v.Bot); err != nil {
				break
			}
			i := positions[id]
			batch[i].Participants = append(batch[i].Participants, v)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
