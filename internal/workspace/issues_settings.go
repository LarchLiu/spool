package workspace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

type IssuesAutomation struct {
	Enabled         bool              `json:"enabled"`
	IntervalMinutes int               `json:"interval_minutes"`
	Repository      string            `json:"repository"` // Derived from origin on save; pins subsequent run checks.
	UserID          string            `json:"user_id"`
	AssigneeIDs     []string          `json:"assignee_ids,omitempty"`
	Backend         chatmodel.Backend `json:"backend"`
	Model           string            `json:"model,omitempty"`
	Effort          string            `json:"effort,omitempty"`
	Permission      string            `json:"permission,omitempty"`
	NextRunAt       int64             `json:"-"`
	LastRunAt       int64             `json:"-"`
	LastError       string            `json:"-"`
	LastChatID      string            `json:"-"`
}

func (v IssuesAutomation) Validate(members []string) error {
	if !v.Enabled {
		return nil
	}
	if v.IntervalMinutes < 1 || v.IntervalMinutes > 10080 {
		return fmt.Errorf("issue interval must be between 1 and 10080 minutes")
	}
	if !issues.ValidRepository(v.Repository) {
		return fmt.Errorf("GitHub repository must be owner/repo")
	}
	if !v.Backend.Valid() {
		return fmt.Errorf("choose an issue automation agent")
	}
	found := false
	for _, id := range members {
		found = found || id == v.UserID
	}
	if !found {
		return fmt.Errorf("issue automation chat owner must be a project member")
	}
	for _, id := range v.Controllers() {
		found = false
		for _, member := range members {
			found = found || member == id
		}
		if !found {
			return fmt.Errorf("all issue automation assignees must be project members")
		}
	}
	found = false
	for _, id := range v.Controllers() {
		found = found || id == v.UserID
	}
	if !found {
		return fmt.Errorf("automation chat owner must be one of its assignees")
	}
	if err := chatmodel.ValidatePermissionMode(v.Backend, v.Permission); err != nil {
		return err
	}
	if err := chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: v.Model, ReasoningEffort: v.Effort}); err != nil {
		return err
	}
	if v.Backend == chatmodel.BackendClaude {
		return chatmodel.ValidateClaudeEffort(v.Effort)
	}
	return nil
}

func (v IssuesAutomation) Controllers() []string {
	if len(v.AssigneeIDs) > 0 {
		return append([]string(nil), v.AssigneeIDs...)
	}
	if v.UserID != "" {
		return []string{v.UserID}
	}
	return nil
}

func (s *Store) IssuesAutomation(project string) (IssuesAutomation, error) {
	v := IssuesAutomation{IntervalMinutes: 60, Backend: chatmodel.BackendCodex}
	var body string
	err := s.db.QueryRow(`SELECT body,next_run_at,last_run_at,last_error,last_chat_id FROM project_automations WHERE project_id=?`, project).Scan(&body, &v.NextRunAt, &v.LastRunAt, &v.LastError, &v.LastChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(body), &v)
	return v, err
}

func saveIssuesAutomation(tx *sql.Tx, project string, v IssuesAutomation) error {
	v.Repository = strings.TrimSpace(v.Repository)
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	next := time.Now().Add(time.Duration(v.IntervalMinutes) * time.Minute).UnixMilli()
	_, err = tx.Exec(`INSERT INTO project_automations(project_id,body,next_run_at) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET body=excluded.body,next_run_at=CASE WHEN body=excluded.body THEN next_run_at ELSE excluded.next_run_at END`, project, string(body), next)
	if err != nil {
		return err
	}
	// Responsibility changes apply to existing task chats, never regular chats.
	if _, err = tx.Exec(`DELETE FROM chat_collaborators WHERE session_id IN (SELECT session_id FROM automation_chats WHERE project_id=?)`, project); err != nil {
		return err
	}
	for _, id := range v.Controllers() {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO chat_collaborators SELECT session_id,? FROM automation_chats WHERE project_id=? AND EXISTS (SELECT 1 FROM project_members WHERE project_id=? AND user_id=?)`, id, project, project, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE sessions SET updated_at=MAX(updated_at+1,?) WHERE id IN (SELECT session_id FROM automation_chats WHERE project_id=?)`, time.Now().UnixMilli(), project)
	return err
}

func (s *Store) createAutomationChat(v IssuesAutomation, project string) (chatmodel.Session, error) {
	var chat chatmodel.Session
	tx, err := s.db.Begin()
	if err != nil {
		return chat, err
	}
	defer tx.Rollback()
	var body string
	if err = tx.QueryRow(`SELECT body FROM project_automations WHERE project_id=?`, project).Scan(&body); err != nil {
		return chat, err
	}
	var current IssuesAutomation
	if err = json.Unmarshal([]byte(body), &current); err != nil {
		return chat, err
	}
	if !current.Enabled {
		return chat, fmt.Errorf("issue automation was disabled before task admission")
	}
	v.UserID, v.AssigneeIDs = current.UserID, current.Controllers()
	if len(v.AssigneeIDs) == 0 {
		return chat, fmt.Errorf("请至少添加一名任务负责人")
	}
	for _, id := range v.AssigneeIDs {
		var count int
		if err = tx.QueryRow(`SELECT count(*) FROM project_members m JOIN users u ON u.id=m.user_id JOIN projects p ON p.id=m.project_id WHERE m.project_id=? AND m.user_id=? AND u.disabled=0 AND p.archived=0`, project, id).Scan(&count); err != nil {
			return chat, err
		}
		if count != 1 {
			return chat, fmt.Errorf("任务负责人已禁用或没有项目权限，请更新负责人")
		}
	}
	chat = chatmodel.Session{ID: chatmodel.NewID(), UserID: v.UserID, ProjectID: project, Title: "New chat", Backend: v.Backend, Status: chatmodel.StatusReady, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli(), AutomationChat: true, CollaboratorIDs: v.Controllers(), Turns: []chatmodel.Turn{}, Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{}}}
	if err = insertSession(tx, chat); err != nil {
		return chat, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_chats VALUES(?,?)`, chat.ID, project); err != nil {
		return chat, err
	}
	for _, id := range v.Controllers() {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO chat_collaborators VALUES(?,?)`, chat.ID, id); err != nil {
			return chat, err
		}
	}
	return chat, tx.Commit()
}

type chatMetadataReader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func readChatCollaborators(db chatMetadataReader, chat string) (bool, []string, error) {
	var task bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM automation_chats WHERE session_id=?)`, chat).Scan(&task); err != nil {
		return false, nil, err
	}
	if !task {
		return false, nil, nil
	}
	rows, err := db.Query(`SELECT user_id FROM chat_collaborators WHERE session_id=? ORDER BY user_id`, chat)
	if err != nil {
		return true, nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return true, nil, err
		}
		ids = append(ids, id)
	}
	return true, ids, rows.Err()
}

func (s *Store) ChatArtifacts(chat string) ([]chatmodel.Artifact, error) {
	return readChatArtifacts(s.db, chat)
}

func readChatArtifacts(db chatMetadataReader, chat string) ([]chatmodel.Artifact, error) {
	rows, err := db.Query(`SELECT type,url FROM chat_artifacts WHERE session_id=? ORDER BY created_at,url`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chatmodel.Artifact
	for rows.Next() {
		var v chatmodel.Artifact
		if err = rows.Scan(&v.Type, &v.URL); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) attachPR(chat, link string) error {
	if !issues.ValidPRURL(link) {
		return fmt.Errorf("invalid PR URL")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT OR IGNORE INTO chat_artifacts VALUES(?,'pull_request',?,?)`, chat, link, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		if _, err = tx.Exec(`UPDATE sessions SET updated_at=MAX(updated_at+1,?) WHERE id=?`, time.Now().UnixMilli(), chat); err != nil {
			return err
		}
	}
	return tx.Commit()
}
