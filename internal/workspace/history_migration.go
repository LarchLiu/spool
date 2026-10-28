package workspace

import (
	"database/sql"
	"errors"
	"fmt"
)

func migrateHistorySchema(db *sql.DB) error {
	var version string
	err := db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if version != "" && version != "1" && version != "2" {
		return fmt.Errorf("unsupported database schema %s", version)
	}
	if version == "2" {
		return nil
	}
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var index, notNull, pk int
		var name, kind string
		var value any
		if err := rows.Scan(&index, &name, &kind, &notNull, &value, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !columns["permission_mode"] {
		if _, err = tx.Exec(`ALTER TABLE sessions ADD COLUMN permission_mode TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	var hasTurns int
	if err = tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='turns'`).Scan(&hasTurns); err != nil {
		return err
	}
	if hasTurns > 0 {
		// Retain the current preference, not historical per-turn settings.
		if _, err = tx.Exec(`UPDATE sessions SET permission_mode=COALESCE((SELECT json_extract(body,'$.permission_mode') FROM turns WHERE session_id=sessions.id ORDER BY sequence DESC LIMIT 1),'')`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DROP TABLE IF EXISTS attachments; DROP TABLE IF EXISTS turns; DROP TABLE IF EXISTS session_history; DROP TABLE IF EXISTS session_sources;
INSERT INTO meta VALUES('schema_version','2') ON CONFLICT(key) DO UPDATE SET value='2'`); err != nil {
		return err
	}
	return tx.Commit()
}
