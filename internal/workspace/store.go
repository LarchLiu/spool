package workspace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

var errChatAgentLocked = errors.New("The agent is fixed after the first message. Create a new chat to use another agent.")
var errTurnChanged = errors.New("turn changed while agent was running")

type Store struct {
	mu      sync.Mutex
	agentMu sync.Mutex
	root    string
	db      *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT NOT NULL UNIQUE COLLATE NOCASE,nickname TEXT NOT NULL,password_hash TEXT NOT NULL,disabled INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,path TEXT NOT NULL UNIQUE,archived INTEGER NOT NULL DEFAULT 0,shared_chats INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS project_members(project_id TEXT NOT NULL REFERENCES projects(id),user_id TEXT NOT NULL REFERENCES users(id),PRIMARY KEY(project_id,user_id));
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,user_id TEXT REFERENCES users(id),project_id TEXT REFERENCES projects(id),title TEXT NOT NULL,backend TEXT NOT NULL,native_session_id TEXT NOT NULL DEFAULT '',native_session_path TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',reasoning_effort TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS sessions_owner ON sessions(user_id,project_id,updated_at DESC);
CREATE TABLE IF NOT EXISTS turns(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),sequence INTEGER NOT NULL,body TEXT NOT NULL,UNIQUE(session_id,sequence));
CREATE TABLE IF NOT EXISTS attachments(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),turn_id TEXT NOT NULL REFERENCES turns(id),name TEXT NOT NULL,media_type TEXT NOT NULL,data BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS attachments_session ON attachments(session_id);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS agents(backend TEXT PRIMARY KEY,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS auth_sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),csrf TEXT NOT NULL,expires_at INTEGER NOT NULL);
`

func OpenStore(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(abs, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, "spool.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Store, error) { db.Close(); return nil, e }
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;`); err != nil {
		return fail(err)
	}
	if _, err = db.Exec(schema); err != nil {
		return fail(err)
	}
	var version string
	err = db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	if version != "" && version != "1" {
		return fail(fmt.Errorf("unsupported database schema %s", version))
	}
	if _, err = db.Exec(`INSERT INTO meta VALUES('schema_version','1') ON CONFLICT(key) DO UPDATE SET value='1'`); err != nil {
		return fail(err)
	}
	return &Store{root: root, db: db}, nil
}
func (s *Store) Close() error {
	// WAL/SHM files are expected while SQLite is open. Checkpoint on a clean
	// shutdown so a stopped desktop process does not leave avoidable sidecars
	// behind; SQLite will recreate them automatically on the next write.
	_, checkpointErr := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	closeErr := s.db.Close()
	return errors.Join(checkpointErr, closeErr)
}
func (s *Store) Root() string { return s.root }
func insertSession(tx *sql.Tx, s chatmodel.Session) error {
	var owner, project any
	if s.UserID != "" {
		owner = s.UserID
	}
	if s.ProjectID != "" {
		project = s.ProjectID
	}
	_, err := tx.Exec(`INSERT INTO sessions(id,user_id,project_id,title,backend,native_session_id,native_session_path,model,reasoning_effort,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, s.ID, owner, project, s.Title, s.Backend, s.NativeSessionID, s.NativeSessionPath, s.Model, s.ReasoningEffort, s.Status, s.CreatedAt, s.UpdatedAt)
	return err
}
func saveTurn(tx *sql.Tx, id string, sequence int, t chatmodel.Turn) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO turns(id,session_id,sequence,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body`, t.ID, id, sequence, string(b))
	return err
}

const sessionColumns = `id,COALESCE(user_id,''),COALESCE(project_id,''),title,backend,native_session_id,native_session_path,model,reasoning_effort,status,created_at,updated_at`

func scanSession(row interface{ Scan(...any) error }) (chatmodel.Session, error) {
	var s chatmodel.Session
	err := row.Scan(&s.ID, &s.UserID, &s.ProjectID, &s.Title, &s.Backend, &s.NativeSessionID, &s.NativeSessionPath, &s.Model, &s.ReasoningEffort, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	s.Turns = []chatmodel.Turn{}
	s.Transcript.Parts = []chatmodel.Part{}
	return s, err
}
func (s *Store) Get(id string) (chatmodel.Session, bool) {
	v, err := scanSession(s.db.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id=?`, id))
	if err != nil {
		return chatmodel.Session{}, false
	}
	rows, err := s.db.Query(`SELECT body FROM turns WHERE session_id=? ORDER BY sequence`, id)
	if err != nil {
		return chatmodel.Session{}, false
	}
	var turns []chatmodel.Turn
	for rows.Next() {
		var b string
		var t chatmodel.Turn
		if rows.Scan(&b) != nil || json.Unmarshal([]byte(b), &t) != nil {
			rows.Close()
			return chatmodel.Session{}, false
		}
		turns = append(turns, t)
		v.PermissionMode = t.PermissionMode
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return chatmodel.Session{}, false
	}
	// Chat text and tool output are intentionally not stored in SQLite. A
	// completed session is hydrated from its provider-owned native JSONL file.
	v.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{}}
	v.Turns = turns
	v = s.hydrateSession(v)
	for i := range v.Turns {
		turn := &v.Turns[i]
		if turn.PartStart < 0 || turn.PartEnd < turn.PartStart || turn.PartEnd > len(v.Transcript.Parts) {
			turn.Parts = nil
			populateTurnContent(turn)
			continue
		}
		turn.Parts = append([]chatmodel.Part(nil), v.Transcript.Parts[turn.PartStart:turn.PartEnd]...)
		populateTurnContent(turn)
	}
	return v, true
}
func (s *Store) Sessions() []chatmodel.Session {
	rows, err := s.db.Query(`SELECT ` + sessionColumns + ` FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil
	}
	var out []chatmodel.Session
	for rows.Next() {
		v, e := scanSession(rows)
		if e == nil {
			out = append(out, v)
		}
	}
	rows.Close()
	for i := range out {
		out[i], _ = s.Get(out[i].ID)
	}
	return out
}

func (s *Store) createSession(user, project string, backend chatmodel.Backend) (chatmodel.Session, error) {
	if !backend.Valid() {
		return chatmodel.Session{}, fmt.Errorf("unsupported agent backend")
	}
	v := chatmodel.Session{ID: chatmodel.NewID(), UserID: user, ProjectID: project, Title: "New chat", Backend: backend, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli(), Status: chatmodel.StatusReady, Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{}}, Turns: []chatmodel.Turn{}}
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if err = insertSession(tx, v); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *Store) BeginTurn(id, prompt string, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
	return s.beginTurn(id, prompt, "", options...)
}

func (s *Store) beginTurn(id, prompt string, backend chatmodel.Backend, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
	return s.beginTurnWithInput(id, prompt, backend, "", nil, options...)
}

func (s *Store) beginTurnWithInput(id, prompt string, backend chatmodel.Backend, permission string, files []uploadedAttachment, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Get(id)
	if !ok {
		return chatmodel.Turn{}, os.ErrNotExist
	}
	if v.Status == chatmodel.StatusRunning {
		return chatmodel.Turn{}, fmt.Errorf("this chat already has a running turn")
	}
	if backend == "" {
		backend = v.Backend
	}
	if !backend.Valid() {
		return chatmodel.Turn{}, fmt.Errorf("unsupported agent backend")
	}
	if backend != v.Backend && len(v.Turns) > 0 {
		return chatmodel.Turn{}, errChatAgentLocked
	}
	if (strings.TrimSpace(prompt) == "" && len(files) == 0) || len(prompt) > chatmodel.MaxMessageBytes {
		return chatmodel.Turn{}, fmt.Errorf("message must be nonempty and at most 128 KB")
	}
	choice := chatmodel.CodexOptions{Model: v.Model, ReasoningEffort: v.ReasoningEffort}
	if backend != v.Backend {
		choice = chatmodel.CodexOptions{}
	}
	if len(options) > 0 {
		choice = options[0]
	}
	if err := chatmodel.ValidateCodexOptions(choice); err != nil {
		return chatmodel.Turn{}, err
	}
	if backend == chatmodel.BackendClaude {
		if err := chatmodel.ValidateClaudeEffort(choice.ReasoningEffort); err != nil {
			return chatmodel.Turn{}, err
		}
	}
	if permission == "" && backend == v.Backend {
		permission = v.PermissionMode
	}
	if permission == "" {
		permission = chatmodel.DefaultPermissionMode(backend)
	}
	if err := chatmodel.ValidatePermissionMode(backend, permission); err != nil {
		return chatmodel.Turn{}, err
	}
	t := chatmodel.Turn{ID: chatmodel.NewID(), Backend: backend, PartStart: len(v.Transcript.Parts), StartedAt: nextSessionTime(v.UpdatedAt), Status: chatmodel.StatusRunning, RequestedModel: choice.Model, ReasoningEffort: choice.ReasoningEffort}
	t.Prompt = prompt
	t.PermissionMode = permission
	for _, file := range files {
		t.Attachments = append(t.Attachments, file.Attachment)
	}
	if strings.TrimSpace(prompt) != "" {
		appendSessionPart(&v.Transcript, &t, chatmodel.Part{Role: "user", Kind: "text", Text: prompt})
	}
	for _, file := range files {
		part := chatmodel.Part{Role: "user", Kind: "context", Name: file.Name, Text: file.Name}
		if file.MediaType != "" && strings.HasPrefix(file.MediaType, "image/") {
			part.Kind, part.Text = "image", "[image]"
		}
		appendSessionPart(&v.Transcript, &t, part)
	}
	t.InputEnd = len(v.Transcript.Parts)
	t.PartEnd = t.InputEnd
	if len(v.Turns) == 0 {
		v.Title = titleFor(prompt)
		if strings.TrimSpace(prompt) == "" && len(files) > 0 {
			v.Title = titleFor(files[0].Name)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return chatmodel.Turn{}, err
	}
	defer tx.Rollback()
	if backend != v.Backend {
		// Only an empty chat may choose a different provider. Never reuse a
		// provider-specific resume ID with that initial selection.
		v.NativeSessionID = ""
		v.NativeSessionPath = ""
	}
	res, err := tx.Exec(`UPDATE sessions SET title=?,status=?,updated_at=?,model=?,reasoning_effort=?,backend=?,native_session_id=?,native_session_path=? WHERE id=? AND status<>?`, v.Title, chatmodel.StatusRunning, t.StartedAt, choice.Model, choice.ReasoningEffort, backend, v.NativeSessionID, v.NativeSessionPath, id, chatmodel.StatusRunning)
	if err != nil {
		return chatmodel.Turn{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return chatmodel.Turn{}, fmt.Errorf("this chat already has a running turn")
	}
	if err = saveTurn(tx, id, len(v.Turns), t); err != nil {
		return chatmodel.Turn{}, err
	}
	for _, file := range files {
		if _, err = tx.Exec(`INSERT INTO attachments(id,session_id,turn_id,name,media_type,data) VALUES(?,?,?,?,?,?)`, file.ID, id, t.ID, file.Name, file.MediaType, file.Data); err != nil {
			return chatmodel.Turn{}, err
		}
	}
	return t, tx.Commit()
}
func (s *Store) SetNativeSession(id, native string) error {
	if !chatmodel.ValidID(native) {
		return fmt.Errorf("invalid provider session ID")
	}
	_, err := s.db.Exec(`UPDATE sessions SET native_session_id=? WHERE id=?`, native, id)
	return err
}

func (s *Store) SetNativeSessionPath(id, native, path string) error {
	if !chatmodel.ValidID(native) {
		return fmt.Errorf("invalid provider session ID")
	}
	_, err := s.db.Exec(`UPDATE sessions SET native_session_id=?,native_session_path=? WHERE id=?`, native, path, id)
	return err
}
func (s *Store) FinishTurn(id, turnID string, result agent.AgentResult, runErr error, status chatmodel.SessionStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Get(id)
	if !ok || len(v.Turns) == 0 {
		return os.ErrNotExist
	}
	t := v.Turns[len(v.Turns)-1]
	if t.ID != turnID {
		return errTurnChanged
	}
	// A stop request may persist the terminal state before the provider
	// process has returned. The provider goroutine will still call FinishTurn;
	// do not let that late result overwrite the explicit stopped state.
	if t.Status != chatmodel.StatusRunning {
		return nil
	}
	// The response itself remains in the provider's native session. Keep only
	// turn metadata in SQLite; the live in-memory turn is what the SSE stream
	// uses until the provider finishes writing its JSONL record.
	t.Parts = nil
	t.PartStart, t.InputEnd, t.PartEnd = 0, 0, 0
	t.Prompt, t.Reply, t.Activities = "", "", nil
	t.PendingApproval = nil
	t.Model = chatmodel.Clip(result.Model, 240)
	if result.ReasoningEffort != "" {
		t.ReasoningEffort = result.ReasoningEffort
	}
	t.Activities = boundedActivities(result.Activities)
	t.Status = status
	t.FinishedAt = nextSessionTime(v.UpdatedAt)
	if runErr != nil {
		t.Error = chatmodel.Clip(runErr.Error(), 8192)
	}
	if chatmodel.ValidID(result.SessionID) {
		v.NativeSessionID = result.SessionID
	}
	// Resolve the project path before opening the write transaction. The
	// database uses one connection; querying the store again while the
	// transaction is active would wait forever for that same connection and
	// leave the chat permanently marked as running.
	nativePath := v.NativeSessionPath
	workingDirectory := ""
	if chatmodel.ValidID(v.NativeSessionID) {
		if v.ProjectID != "" {
			if project, e := s.Project(v.ProjectID); e == nil {
				workingDirectory = project.Path
			}
		}
		if path, e := agent.FindSessionTranscript(v.Backend, v.NativeSessionID, workingDirectory); e == nil {
			nativePath = path
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveTurn(tx, id, len(v.Turns)-1, t); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE sessions SET native_session_id=?,native_session_path=?,status=?,updated_at=? WHERE id=?`, v.NativeSessionID, nativePath, status, t.FinishedAt, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveRunningTurn checkpoints lifecycle metadata while a CLI is still
// streaming. The normalized transcript stays in App.live and is never copied
// into SQLite.
func (s *Store) SaveRunningTurn(id string, turn chatmodel.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Get(id)
	if !ok || len(v.Turns) == 0 || v.Turns[len(v.Turns)-1].ID != turn.ID {
		return os.ErrNotExist
	}
	if v.Status != chatmodel.StatusRunning {
		return fmt.Errorf("turn is no longer running")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveTurn(tx, id, len(v.Turns)-1, turn); err != nil {
		return err
	}
	return tx.Commit()
}

// updated_at also serves as the SSE cache revision, so every transition must
// advance even when multiple writes land in one millisecond or the clock moves.
func nextSessionTime(previous int64) int64 {
	return max(time.Now().UnixMilli(), previous+1)
}

func appendSessionPart(transcript *chatmodel.Transcript, turn *chatmodel.Turn, part chatmodel.Part) bool {
	before := len(transcript.Parts)
	more := chatmodel.AppendPart(transcript, part)
	if len(transcript.Parts) > before {
		turn.Parts = append(turn.Parts, transcript.Parts[len(transcript.Parts)-1])
		turn.PartEnd = turn.PartStart + len(turn.Parts)
	}
	return more
}

func appendTurnPart(turn *chatmodel.Turn, part chatmodel.Part) bool {
	transcript := chatmodel.Transcript{Parts: turn.Parts}
	before := len(turn.Parts)
	// Command output may arrive as multiple deltas. Merge adjacent result
	// chunks so the live transcript keeps the same shape as native history.
	more := true
	if part.Kind == "tool_result" {
		chatmodel.AppendStreamText(&transcript, part)
	} else {
		more = chatmodel.AppendPart(&transcript, part)
	}
	if len(transcript.Parts) > before {
		turn.Parts = transcript.Parts
		turn.PartEnd = turn.PartStart + len(turn.Parts)
	}
	return more
}

func appendTurnText(turn *chatmodel.Turn, part chatmodel.Part) {
	transcript := chatmodel.Transcript{Parts: turn.Parts}
	chatmodel.AppendStreamText(&transcript, part)
	turn.Parts = transcript.Parts
	turn.PartEnd = turn.PartStart + len(turn.Parts)
}

func replacePartRange(parts []chatmodel.Part, start, end int, replacement []chatmodel.Part) []chatmodel.Part {
	start = max(0, min(start, len(parts)))
	end = max(start, min(end, len(parts)))
	out := make([]chatmodel.Part, 0, len(parts)-(end-start)+len(replacement))
	out = append(out, parts[:start]...)
	out = append(out, replacement...)
	out = append(out, parts[end:]...)
	return out
}

func runningTranscript(previous chatmodel.Transcript, turn chatmodel.Turn) chatmodel.Transcript {
	start := max(0, min(turn.PartStart, len(previous.Parts)))
	transcript := chatmodel.Transcript{
		Parts: append([]chatmodel.Part(nil), previous.Parts[:start]...),
		Cut:   previous.Cut,
	}
	for _, part := range turn.Parts {
		if part.Role == "assistant" && part.Kind == "text" {
			chatmodel.AppendStreamText(&transcript, part)
		} else if !chatmodel.AppendPart(&transcript, part) {
			break
		}
	}
	return transcript
}

func cloneTranscript(transcript chatmodel.Transcript) chatmodel.Transcript {
	clone := transcript
	clone.Parts = append([]chatmodel.Part(nil), transcript.Parts...)
	return clone
}

func populateTurnContent(turn *chatmodel.Turn) {
	var prompt, reply strings.Builder
	activities := make([]string, 0)
	for _, part := range turn.Parts {
		switch {
		case part.Role == "user" && part.Kind == "text":
			prompt.WriteString(part.Text)
		case part.Role == "assistant" && part.Kind == "text":
			reply.WriteString(part.Text)
		case part.Kind == "tool_use" || part.Kind == "tool_result" || part.Kind == "context":
			text := part.Text
			if part.Name != "" && text != "" {
				text = part.Name + ": " + text
			} else if part.Name != "" {
				text = part.Name
			}
			if text != "" {
				activities = append(activities, text)
			}
		}
	}
	turn.Prompt = prompt.String()
	turn.Reply = reply.String()
	turn.Activities = boundedActivities(activities)
}

func (s *Store) SetInterrupted() error {
	for _, v := range s.Sessions() {
		if v.Status == chatmodel.StatusRunning && len(v.Turns) > 0 {
			t := v.Turns[len(v.Turns)-1]
			output := []chatmodel.Part{}
			start := max(0, min(t.InputEnd-t.PartStart, len(t.Parts)))
			if start < len(t.Parts) {
				output = append(output, t.Parts[start:]...)
			}
			if err := s.FinishTurn(v.ID, t.ID, agent.AgentResult{Text: t.Reply, Model: t.Model, Activities: t.Activities, Parts: output}, fmt.Errorf("Go agent server restarted before this turn finished"), chatmodel.StatusInterrupted); err != nil {
				return err
			}
		}
	}
	return nil
}
func titleFor(prompt string) string {
	text := strings.Join(strings.Fields(prompt), " ")
	runes := []rune(text)
	if len(runes) > 52 {
		return string(runes[:51]) + "…"
	}
	if text == "" {
		return "New chat"
	}
	return text
}

func boundedActivities(items []string) []string {
	if len(items) > 100 {
		items = items[len(items)-100:]
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, chatmodel.Clip(strings.TrimSpace(item), 240))
		}
	}
	return out
}
