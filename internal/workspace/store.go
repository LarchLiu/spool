package workspace

import (
	"database/sql"
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

var errSessionConversion = errors.New("Could not convert this chat to another agent. Its native history must be available; the original chat is unchanged.")
var errTurnChanged = errors.New("turn changed while agent was running")

type Store struct {
	changes      changeHub
	historyCache nativeHistoryCache
	settingsMu   sync.Mutex
	mu           sync.Mutex
	agentMu      sync.Mutex
	localMu      sync.Mutex
	localScanAt  time.Time
	localIndex   agent.LocalSessionIndex
	root         string
	db           *sql.DB
	runtimeMu    sync.Mutex
	runtimeTurns map[string]chatmodel.Turn
}

const schema = `
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT NOT NULL UNIQUE COLLATE NOCASE,nickname TEXT NOT NULL,password_hash TEXT NOT NULL,disabled INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS user_avatars(user_id TEXT PRIMARY KEY REFERENCES users(id),style TEXT NOT NULL,seed TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,path TEXT NOT NULL UNIQUE,archived INTEGER NOT NULL DEFAULT 0,shared_chats INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS project_members(project_id TEXT NOT NULL REFERENCES projects(id),user_id TEXT NOT NULL REFERENCES users(id),PRIMARY KEY(project_id,user_id));
CREATE TABLE IF NOT EXISTS project_member_access(project_id TEXT NOT NULL,user_id TEXT NOT NULL,access TEXT NOT NULL CHECK(access IN ('read-only','control')),PRIMARY KEY(project_id,user_id),FOREIGN KEY(project_id,user_id) REFERENCES project_members(project_id,user_id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS project_member_own_access(project_id TEXT NOT NULL,user_id TEXT NOT NULL,access TEXT NOT NULL CHECK(access IN ('read-only','create')),PRIMARY KEY(project_id,user_id),FOREIGN KEY(project_id,user_id) REFERENCES project_members(project_id,user_id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS project_local_sharing(project_id TEXT PRIMARY KEY REFERENCES projects(id),enabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,user_id TEXT REFERENCES users(id),project_id TEXT REFERENCES projects(id),title TEXT NOT NULL,backend TEXT NOT NULL,native_session_id TEXT NOT NULL DEFAULT '',native_session_path TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',reasoning_effort TEXT NOT NULL DEFAULT '',permission_mode TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS sessions_owner ON sessions(user_id,project_id,updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_native ON sessions(backend,native_session_id);
CREATE TABLE IF NOT EXISTS local_sessions(session_id TEXT PRIMARY KEY REFERENCES sessions(id),visible INTEGER NOT NULL DEFAULT 1,directory TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS local_session_files(session_id TEXT PRIMARY KEY REFERENCES local_sessions(session_id),generation TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS spool_native_sessions(backend TEXT NOT NULL,native_session_id TEXT NOT NULL,PRIMARY KEY(backend,native_session_id));
CREATE TABLE IF NOT EXISTS session_workspaces(session_id TEXT PRIMARY KEY REFERENCES sessions(id),directory TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS agents(backend TEXT PRIMARY KEY,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS project_automations(project_id TEXT PRIMARY KEY REFERENCES projects(id),body TEXT NOT NULL,next_run_at INTEGER NOT NULL,last_run_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',last_chat_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS automation_runs(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id),instance TEXT NOT NULL,chat_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,started_at INTEGER NOT NULL,finished_at INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS automation_runs_chat ON automation_runs(chat_id,project_id);
CREATE TABLE IF NOT EXISTS chat_artifacts(session_id TEXT NOT NULL REFERENCES sessions(id),type TEXT NOT NULL,url TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(session_id,type,url));
CREATE TABLE IF NOT EXISTS automation_chats(session_id TEXT PRIMARY KEY REFERENCES sessions(id),project_id TEXT NOT NULL REFERENCES projects(id));
CREATE TABLE IF NOT EXISTS chat_collaborators(session_id TEXT NOT NULL REFERENCES sessions(id),user_id TEXT NOT NULL REFERENCES users(id),PRIMARY KEY(session_id,user_id));
CREATE TABLE IF NOT EXISTS turn_actors(session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,turn_id TEXT NOT NULL,user_id TEXT REFERENCES users(id),actor_kind TEXT NOT NULL DEFAULT 'user',native_session_id TEXT NOT NULL DEFAULT '',native_turn_id TEXT NOT NULL DEFAULT '',source_offset INTEGER NOT NULL DEFAULT -1,started_at INTEGER NOT NULL,PRIMARY KEY(session_id,turn_id));
CREATE TABLE IF NOT EXISTS turn_actions(session_id TEXT NOT NULL,turn_id TEXT NOT NULL,id TEXT NOT NULL,kind TEXT NOT NULL,user_id TEXT NOT NULL REFERENCES users(id),approval_id TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,PRIMARY KEY(session_id,id),UNIQUE(session_id,turn_id,kind,approval_id),FOREIGN KEY(session_id,turn_id) REFERENCES turn_actors(session_id,turn_id) ON DELETE CASCADE);
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
	path, err := prepareDatabase(root)
	if err != nil {
		return nil, err
	}
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
	if err = migrateHistorySchema(db); err != nil {
		return fail(err)
	}
	if err = migrateTurnActorSchema(db); err != nil {
		return fail(fmt.Errorf("migrate turn actors: %w", err))
	}
	// Retain every known Spool origin, even after a chat adopts another ID.
	if _, err = db.Exec(`INSERT OR IGNORE INTO spool_native_sessions SELECT backend,native_session_id FROM sessions WHERE native_session_id<>'' AND NOT EXISTS(SELECT 1 FROM local_sessions WHERE session_id=sessions.id);
		INSERT OR IGNORE INTO spool_native_sessions SELECT s.backend,t.native_session_id FROM turn_actors t JOIN sessions s ON s.id=t.session_id WHERE t.native_session_id<>'' AND NOT EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id);`); err != nil {
		return fail(err)
	}
	// Retired avatar styles adopt Clay without losing automatic or picked seeds.
	if _, err = db.Exec(`UPDATE user_avatars SET style=? WHERE style IN ('bottts-neutral','shapes','initials')`, DefaultAvatar().Style); err != nil {
		return fail(err)
	}
	store := &Store{root: root, db: db, runtimeTurns: make(map[string]chatmodel.Turn)}
	settings, err := store.Settings()
	if err != nil {
		return fail(err)
	}
	if err := validateHistoryCacheMiB(settings.HistoryCacheMiB); err != nil {
		return fail(err)
	}
	store.historyCache.setMaxBytes(settings.HistoryCacheMiB * 1024 * 1024)
	return store, nil
}
func (s *Store) Close() error {
	// WAL/SHM files are expected while SQLite is open. Checkpoint on a clean
	// shutdown so a stopped desktop process does not leave avoidable sidecars
	// behind; SQLite will recreate them automatically on the next write.
	_, checkpointErr := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	closeErr := s.db.Close()
	s.historyCache.clear()
	return errors.Join(checkpointErr, closeErr)
}
func (s *Store) Root() string         { return s.root }
func (s *Store) DatabasePath() string { return databasePath(s.root) }
func insertSession(tx *sql.Tx, s chatmodel.Session) error {
	var owner, project any
	if s.UserID != "" {
		owner = s.UserID
	}
	if s.ProjectID != "" {
		project = s.ProjectID
	}
	_, err := tx.Exec(`INSERT INTO sessions(id,user_id,project_id,title,backend,native_session_id,native_session_path,model,reasoning_effort,permission_mode,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, s.ID, owner, project, s.Title, s.Backend, s.NativeSessionID, s.NativeSessionPath, s.Model, s.ReasoningEffort, s.PermissionMode, s.Status, s.CreatedAt, s.UpdatedAt)
	if err == nil {
		err = registerSpoolNative(tx, s.ID)
	}
	return err
}

func registerSpoolNative(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO spool_native_sessions SELECT backend,native_session_id FROM sessions WHERE id=? AND native_session_id<>'' AND NOT EXISTS(SELECT 1 FROM local_sessions WHERE session_id=sessions.id)`, id)
	return err
}

const sessionColumns = `id,COALESCE(user_id,''),COALESCE(project_id,''),title,backend,native_session_id,native_session_path,model,reasoning_effort,permission_mode,status,created_at,updated_at,EXISTS(SELECT 1 FROM local_sessions WHERE session_id=sessions.id)`

func scanSession(row interface{ Scan(...any) error }) (chatmodel.Session, error) {
	var s chatmodel.Session
	err := row.Scan(&s.ID, &s.UserID, &s.ProjectID, &s.Title, &s.Backend, &s.NativeSessionID, &s.NativeSessionPath, &s.Model, &s.ReasoningEffort, &s.PermissionMode, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.LocalSession)
	s.Turns = []chatmodel.Turn{}
	s.Transcript.Parts = []chatmodel.Part{}
	return s, err
}
func (s *Store) Get(id string) (chatmodel.Session, bool) {
	v, ok := s.getSession(id)
	if !ok {
		return v, false
	}
	stored := v
	v = s.hydrateSession(v)
	if stored.AutomationChat {
		// Issue chats deliberately retain their issue-number/title label.
		v.Title = stored.Title
	} else if v.Title != stored.Title {
		// A native title is metadata, not new conversation activity. Preserve
		// ordering and never overwrite a concurrently switched native session.
		result, err := s.db.Exec(`UPDATE sessions SET title=? WHERE id=? AND title=? AND backend=? AND native_session_id=? AND native_session_path=?`, v.Title, id, stored.Title, stored.Backend, stored.NativeSessionID, stored.NativeSessionPath)
		if err != nil {
			v.Title = stored.Title
		} else if n, _ := result.RowsAffected(); n == 0 {
			if latest, found := s.getSession(id); found {
				v.Title = latest.Title
			}
		} else {
			s.changes.publish(changeNavigation)
		}
	}
	v = s.withRuntimeTurn(v)
	if err := s.attributeTurns(&v); err != nil {
		v.History = &chatmodel.HistoryState{Error: "Turn attribution is unavailable"}
	}
	return v, true
}

func (s *Store) getSession(id string) (chatmodel.Session, bool) {
	// Read metadata and its SSE revision from one SQLite snapshot.
	tx, err := s.db.Begin()
	if err != nil {
		return chatmodel.Session{}, false
	}
	defer tx.Rollback()
	v, err := scanSession(tx.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id=?`, id))
	if err != nil {
		return chatmodel.Session{}, false
	}
	v.Artifacts, err = readChatArtifacts(tx, id)
	if err != nil {
		return chatmodel.Session{}, false
	}
	v.AutomationChat, v.CollaboratorIDs, err = readChatCollaborators(tx, id)
	if err != nil {
		return chatmodel.Session{}, false
	}
	chats := []chatmodel.Session{v}
	if err = populateParticipants(tx, chats); err != nil {
		return chatmodel.Session{}, false
	}
	v.Participants = chats[0].Participants
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
	return v, s.notify(tx.Commit(), changeNavigation)
}
func (s *Store) BeginTurn(id, prompt string, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
	return s.beginTurn(id, prompt, "", options...)
}

func (s *Store) beginTurn(id, prompt string, backend chatmodel.Backend, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
	return s.beginTurnWithInput(id, prompt, backend, "", nil, options...)
}

func (s *Store) beginTurnWithInput(id, prompt string, backend chatmodel.Backend, permission string, files []uploadedAttachment, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
	return s.beginTurnBy(id, prompt, backend, permission, files, "", options...)
}

func (s *Store) beginTurnBy(id, prompt string, backend chatmodel.Backend, permission string, files []uploadedAttachment, user string, options ...chatmodel.CodexOptions) (chatmodel.Turn, error) {
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
	if backend != v.Backend && (v.NativeSessionID != "" || v.Status != chatmodel.StatusReady) {
		return chatmodel.Turn{}, errSessionConversion
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
	t.UserID = user
	if t.UserID == "" {
		t.UserID = v.UserID
	}
	t.Prompt = prompt
	sourceOffset := int64(-1)
	if info, e := os.Stat(v.NativeSessionPath); e == nil {
		t.SourceOffset = info.Size()
		sourceOffset = info.Size()
	} else if v.NativeSessionID == "" {
		sourceOffset = 0
	}
	t.PermissionMode = permission
	for _, file := range files {
		t.Attachments = append(t.Attachments, file.Attachment)
	}
	input := chatmodel.Transcript{}
	if strings.TrimSpace(prompt) != "" {
		appendSessionPart(&input, &t, chatmodel.Part{Role: "user", Kind: "text", Text: prompt})
	}
	for _, file := range files {
		part := chatmodel.Part{Role: "user", Kind: "context", Name: file.Name, Text: file.Name}
		if file.MediaType != "" && strings.HasPrefix(file.MediaType, "image/") {
			part.Kind, part.Text = "image", "[image]"
		}
		appendSessionPart(&input, &t, part)
	}
	t.InputEnd = t.PartStart + len(t.Parts)
	t.PartEnd = t.InputEnd
	if len(v.Turns) == 0 && (v.Title == "" || v.Title == "New chat") {
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
	res, err := tx.Exec(`UPDATE sessions SET title=?,status=?,updated_at=?,model=?,reasoning_effort=?,permission_mode=?,backend=? WHERE id=? AND status<>?`, v.Title, chatmodel.StatusRunning, t.StartedAt, choice.Model, choice.ReasoningEffort, permission, backend, id, chatmodel.StatusRunning)
	if err != nil {
		return chatmodel.Turn{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return chatmodel.Turn{}, fmt.Errorf("this chat already has a running turn")
	}
	actorUser, actorKind := t.UserID, "user"
	if t.UserID == chatmodel.SpoolBotID {
		actorUser, actorKind = "", "bot"
	}
	if _, err = tx.Exec(`INSERT INTO turn_actors(session_id,turn_id,user_id,actor_kind,native_session_id,source_offset,started_at) VALUES(?,?,NULLIF(?,''),?,?,?,?)`, id, t.ID, actorUser, actorKind, v.NativeSessionID, sourceOffset, t.StartedAt); err != nil {
		return chatmodel.Turn{}, err
	}
	if err = tx.Commit(); err != nil {
		return chatmodel.Turn{}, err
	}
	s.setRuntimeTurn(id, t)
	s.changes.publish(changeNavigation)
	return t, nil
}
func (s *Store) SetNativeSession(id, native string) error {
	return s.setNativeSession(id, native, "", false)
}

func (s *Store) setNativeSession(id, native, path string, withPath bool) error {
	if !chatmodel.ValidID(native) {
		return fmt.Errorf("invalid provider session ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if withPath {
		_, err = tx.Exec(`UPDATE sessions SET native_session_id=?,native_session_path=? WHERE id=?`, native, path, id)
	} else {
		_, err = tx.Exec(`UPDATE sessions SET native_session_id=? WHERE id=?`, native, id)
	}
	if err == nil {
		err = registerSpoolNative(tx, id)
	}
	if err != nil {
		return err
	}
	return s.notify(tx.Commit(), changeNavigation)
}

func (s *Store) setTurnNativeSession(id, turnID, native string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat, ok := s.getSession(id)
	t, active := s.runtimeTurn(id)
	if !ok || !active || chat.Status != chatmodel.StatusRunning || t.ID != turnID {
		return errTurnChanged
	}
	if !chatmodel.ValidID(native) {
		return fmt.Errorf("invalid provider session ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE sessions SET native_session_id=? WHERE id=?`, native, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE turn_actors SET native_session_id=? WHERE session_id=? AND turn_id=?`, native, id, turnID); err != nil {
		return err
	}
	if err = registerSpoolNative(tx, id); err != nil {
		return err
	}
	return s.notify(tx.Commit(), changeNavigation)
}

func (s *Store) SetNativeSessionPath(id, native, path string) error {
	return s.setNativeSession(id, native, path, true)
}
func (s *Store) FinishTurn(id, turnID string, result agent.AgentResult, runErr error, status chatmodel.SessionStatus) error {
	return s.finishTurn(id, turnID, result, runErr, status, "")
}

func (s *Store) finishTurn(id, turnID string, result agent.AgentResult, runErr error, status chatmodel.SessionStatus, stoppedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.getSession(id)
	if !ok {
		return os.ErrNotExist
	}
	t, ok := s.runtimeTurn(id)
	if !ok || t.ID != turnID {
		return errTurnChanged
	}
	if t.Status != chatmodel.StatusRunning {
		return nil
	}
	t.Parts, t.Prompt, t.Reply, t.Activities = nil, "", "", nil
	t.PartStart, t.InputEnd, t.PartEnd = 0, 0, 0
	t.PendingApproval = nil
	if result.TurnID != "" {
		t.NativeTurnID = result.TurnID
	}
	t.Model = chatmodel.Clip(result.Model, 240)
	if result.ReasoningEffort != "" {
		t.ReasoningEffort = result.ReasoningEffort
	}
	t.Status, t.FinishedAt = status, nextSessionTime(v.UpdatedAt)
	if runErr != nil {
		t.Error = chatmodel.Clip(runErr.Error(), 8192)
	}
	if chatmodel.ValidID(result.SessionID) {
		v.NativeSessionID = result.SessionID
	}
	nativePath := v.NativeSessionPath
	if v.NativeSessionID != "" {
		directory := ""
		if project, e := s.Project(v.ProjectID); e == nil {
			directory = project.Path
		}
		if path, e := agent.FindSessionTranscript(v.Backend, v.NativeSessionID, directory); e == nil {
			nativePath = path
		}
	}
	if nativePath != "" {
		if index, e := s.indexHistory(v.Backend, nativePath); e == nil && len(index) > 0 {
			last := index[len(index)-1]
			if last.Start >= t.SourceOffset {
				t.NativeTurnID = nativeIdentity(v.NativeSessionID, last)
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE sessions SET native_session_id=?,native_session_path=?,status=?,updated_at=? WHERE id=?`, v.NativeSessionID, nativePath, status, t.FinishedAt, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE turn_actors SET native_session_id=?,native_turn_id=? WHERE session_id=? AND turn_id=?`, v.NativeSessionID, t.NativeTurnID, id, turnID); err != nil {
		return err
	}
	if err = registerSpoolNative(tx, id); err != nil {
		return err
	}
	if stoppedBy != "" {
		action, err := insertTurnAction(tx, id, turnID, stoppedBy, "stop", "")
		if err != nil {
			return err
		}
		t.Actions = append(t.Actions, action)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.setRuntimeTurn(id, t)
	s.changes.publish(changeNavigation)
	return nil
}

// Lifecycle/transcript state is process-local; only actor metadata is durable.
func (s *Store) SaveRunningTurn(id string, turn chatmodel.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.runtimeTurn(id)
	if !ok || current.ID != turn.ID || current.Status != chatmodel.StatusRunning {
		return errTurnChanged
	}
	if turn.NativeTurnID != current.NativeTurnID {
		if _, err := s.db.Exec(`UPDATE turn_actors SET native_turn_id=? WHERE session_id=? AND turn_id=?`, turn.NativeTurnID, id, turn.ID); err != nil {
			return err
		}
	}
	s.setRuntimeTurn(id, turn)
	return nil
}

func (s *Store) runtimeTurn(id string) (chatmodel.Turn, bool) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	t, ok := s.runtimeTurns[id]
	return t, ok
}

func (s *Store) setRuntimeTurn(id string, turn chatmodel.Turn) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	turn.Parts = append([]chatmodel.Part(nil), turn.Parts...)
	s.runtimeTurns[id] = turn
}

// updated_at also serves as the SSE cache revision, so every transition must
// advance even when multiple writes land in one millisecond or the clock moves.
func nextSessionTime(previous int64) int64 {
	return max(time.Now().UnixMilli(), previous+1)
}

func appendSessionPart(transcript *chatmodel.Transcript, turn *chatmodel.Turn, part chatmodel.Part) {
	before := len(transcript.Parts)
	chatmodel.AppendPart(transcript, part)
	if len(transcript.Parts) > before {
		turn.Parts = append(turn.Parts, transcript.Parts[len(transcript.Parts)-1])
		turn.PartEnd = turn.PartStart + len(turn.Parts)
	}
}

func appendTurnPart(turn *chatmodel.Turn, part chatmodel.Part) {
	transcript := chatmodel.Transcript{Parts: turn.Parts}
	// Command output may arrive as multiple deltas. Merge adjacent result
	// chunks so the live transcript keeps the same shape as native history.
	if part.Kind == "tool_result" {
		chatmodel.AppendStreamText(&transcript, part)
	} else {
		chatmodel.AppendPart(&transcript, part)
	}
	turn.Parts = transcript.Parts
	turn.PartEnd = turn.PartStart + len(turn.Parts)
}

func appendTurnText(turn *chatmodel.Turn, part chatmodel.Part) {
	transcript := chatmodel.Transcript{Parts: turn.Parts}
	chatmodel.AppendStreamText(&transcript, part)
	turn.Parts = transcript.Parts
	turn.PartEnd = turn.PartStart + len(turn.Parts)
}

func runningTranscript(previous chatmodel.Transcript, turn chatmodel.Turn) chatmodel.Transcript {
	start := max(0, min(turn.PartStart, len(previous.Parts)))
	transcript := chatmodel.Transcript{
		Parts: append([]chatmodel.Part(nil), previous.Parts[:start]...),
		Cut:   previous.Cut,
	}
	transcript.Parts = append(transcript.Parts, turn.Parts...)
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
	_, err := s.db.Exec(`UPDATE sessions SET status=?,updated_at=MAX(updated_at+1,?) WHERE status=?`, chatmodel.StatusInterrupted, time.Now().UnixMilli(), chatmodel.StatusRunning)
	if err != nil {
		return err
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	for id, t := range s.runtimeTurns {
		if t.Status == chatmodel.StatusRunning {
			t.Status, t.FinishedAt, t.PendingApproval = chatmodel.StatusInterrupted, time.Now().UnixMilli(), nil
			t.Error = "Go agent server restarted before this turn finished"
			s.runtimeTurns[id] = t
		}
	}
	s.changes.publish(changeNavigation)
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
