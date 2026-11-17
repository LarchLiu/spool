package workspace

import (
	"database/sql"
	"errors"

	chatmodel "spool/internal/model"
)

// Permission choices belong to the Spool project, independently for each agent.
// They never modify the provider's configuration or the modes it makes available.
func (s *Store) projectPermissionMode(project string, backend chatmodel.Backend) (string, error) {
	var mode string
	err := s.db.QueryRow(`SELECT mode FROM project_permission_preferences WHERE project_id=? AND backend=?`, project, backend).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return mode, err
}

func (s *Store) saveProjectPermissionMode(project string, backend chatmodel.Backend, mode string) error {
	if !backend.Valid() || mode == "" {
		return errors.New("Invalid permission mode")
	}
	if err := chatmodel.ValidatePermissionMode(backend, mode); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO project_permission_preferences(project_id,backend,mode) VALUES(?,?,?) ON CONFLICT(project_id,backend) DO UPDATE SET mode=excluded.mode`, project, backend, mode)
	return err
}
