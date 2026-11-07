package workspace

import "database/sql"

// Keep the device's opt-in after natural login expiry. Delivery still requires
// a live session, and only the owning account can rebind it after signing in.
func migrateWebPushSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var hasOwner bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('web_push_subscriptions') WHERE name='user_id')`).Scan(&hasOwner); err != nil {
		return err
	}
	if !hasOwner {
		if _, err := tx.Exec(`CREATE TABLE web_push_subscriptions_next(endpoint TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),token_hash TEXT REFERENCES auth_sessions(token_hash) ON DELETE SET NULL,p256dh TEXT NOT NULL,auth TEXT NOT NULL,language TEXT NOT NULL,updated_at INTEGER NOT NULL,last_test_at INTEGER NOT NULL DEFAULT 0);
INSERT INTO web_push_subscriptions_next(endpoint,user_id,token_hash,p256dh,auth,language,updated_at,last_test_at)
SELECT p.endpoint,a.user_id,p.token_hash,p.p256dh,p.auth,p.language,p.updated_at,p.last_test_at FROM web_push_subscriptions p JOIN auth_sessions a ON a.token_hash=p.token_hash;
DROP TABLE web_push_subscriptions;
ALTER TABLE web_push_subscriptions_next RENAME TO web_push_subscriptions;`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS web_push_session ON web_push_subscriptions(token_hash);
CREATE INDEX IF NOT EXISTS web_push_user ON web_push_subscriptions(user_id);`); err != nil {
		return err
	}
	return tx.Commit()
}
