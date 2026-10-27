package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	resources "spool"
	"spool/internal/agent"
	"spool/internal/i18n"
	chatmodel "spool/internal/model"
)

type App struct {
	store           *Store
	runner          agent.AgentRunner
	settings        Settings
	streamHeartbeat time.Duration
	turnMu          sync.Mutex
	closing         bool
	updating        bool
	runningTurns    int
	turns           sync.WaitGroup
	activeTurns     map[string]*turnControl
	liveMu          sync.Mutex
	live            map[string]chatmodel.Turn
	// liveBase keeps the hydrated provider transcript that existed before the
	// current turn. SQLite intentionally omits transcript bodies, so a running
	// resumed chat needs this base while its provider is streaming new parts.
	liveBase   map[string]chatmodel.Transcript
	completed  map[string]completedTurn
	streams    map[string]int
	streamSeen map[string]bool
	presence   *Presence
	limiter    loginLimiter
	assets     http.Handler
	filesMu    sync.Mutex
	filesRoot  string
	approvals  map[string]*pendingApproval
	shutdown   chan struct{}
	closeOnce  sync.Once
}

type completedTurn struct {
	turn chatmodel.Turn
	base chatmodel.Transcript
}

func NewApp(store *Store, runner agent.AgentRunner, _, _ string) *App {
	settings, _ := store.Settings()
	assets, _ := fs.Sub(resources.Frontend(), "frontend")
	return &App{store: store, runner: runner, settings: settings, streamHeartbeat: 15 * time.Second, live: map[string]chatmodel.Turn{}, liveBase: map[string]chatmodel.Transcript{}, completed: map[string]completedTurn{}, streams: map[string]int{}, streamSeen: map[string]bool{}, approvals: map[string]*pendingApproval{}, shutdown: make(chan struct{}), presence: &Presence{}, assets: http.FileServer(http.FS(assets))}
}
func apiJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, message string) {
	apiJSON(w, status, map[string]string{"error": i18n.Translate(w.Header().Get("Content-Language"), message)})
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self'; manifest-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		preferred := []string{}
		for _, tag := range strings.Split(r.Header.Get("Accept-Language"), ",") {
			parts := strings.Split(strings.TrimSpace(tag), ";")
			if len(parts) == 1 || strings.TrimSpace(parts[1]) != "q=0" {
				preferred = append(preferred, parts[0])
			}
		}
		w.Header().Set("Content-Language", i18n.ResolveLanguage(r.Header.Get("X-UI-Language"), preferred...))
		a.api(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		apiJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if strings.Contains(r.URL.Path, "..") || strings.HasPrefix(r.URL.Path, "/api") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/sw.js" {
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	a.assets.ServeHTTP(w, r)
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/login" && r.Method == "POST" {
		if !a.sameOrigin(r) {
			apiError(w, 403, "跨域登录被拒绝")
			return
		}
		if !a.limiter.allow(r.RemoteAddr) {
			apiError(w, 429, "登录尝试过多，请稍后重试")
			return
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if len(input.Username) > 64 || len(input.Password) > 72 {
			apiError(w, 401, "用户名或密码不正确")
			return
		}
		token, id, err := a.store.login(input.Username, input.Password)
		if err != nil {
			apiError(w, 401, "用户名或密码不正确")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: authCookie, Value: token, Path: "/", HttpOnly: true, Secure: a.secureCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: 7 * 24 * 3600})
		a.signedInResponse(w, id)
		return
	}
	id, err := a.identity(r)
	if err != nil {
		apiError(w, 401, "请先登录")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && !a.authorizeMutation(w, r, id) {
		return
	}
	switch {
	case path == "/api/me" && r.Method == "GET":
		a.signedInResponse(w, id)
	case path == "/api/me/avatar" && r.Method == "POST":
		var avatar Avatar
		if !decodeJSON(w, r, &avatar) {
			return
		}
		if err := avatar.Validate(); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		if err := a.store.SaveAvatar(id.User.ID, avatar); err != nil {
			apiError(w, 500, "Could not save avatar")
			return
		}
		id.User.Avatar = avatar
		apiJSON(w, 200, id.User)
	case path == "/api/preferences" && r.Method == "GET":
		preferences, err := a.store.UIPreferences(id.User.ID)
		if err != nil {
			apiError(w, 500, "Could not save preferences")
			return
		}
		apiJSON(w, 200, preferences)
	case path == "/api/preferences" && r.Method == "POST":
		var preferences UIPreferences
		if !decodeJSON(w, r, &preferences) {
			return
		}
		if err := preferences.Validate(); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		if err := a.store.SaveUIPreferences(id.User.ID, preferences); err != nil {
			apiError(w, 500, "Could not save preferences")
			return
		}
		apiJSON(w, 200, preferences)
	case path == "/api/navigation" && r.Method == "GET":
		navigation, err := a.store.UserNavigation(id.User.ID)
		if err != nil {
			apiError(w, 500, "Could not load navigation")
			return
		}
		apiJSON(w, 200, navigation)
	case path == "/api/logout" && r.Method == "POST":
		if _, err = a.store.db.Exec(`DELETE FROM auth_sessions WHERE token_hash=?`, id.TokenHash); err != nil {
			apiError(w, 500, "退出失败")
			return
		}
		a.presence.Remove(id.TokenHash)
		http.SetCookie(w, &http.Cookie{Name: authCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secureCookie(r), SameSite: http.SameSiteStrictMode})
		apiJSON(w, 200, map[string]bool{"ok": true})
	case path == "/api/projects" && r.Method == "GET":
		projects, err := a.store.UserProjects(id.User.ID)
		if err != nil {
			apiError(w, 500, "读取项目失败")
			return
		}
		apiJSON(w, 200, projects)
	case path == "/api/permissions" && r.Method == "GET":
		a.permissionResponse(w, r, id.User.ID)
	case path == "/api/agents" && r.Method == "GET":
		agents, err := a.store.Agents()
		if err != nil {
			apiError(w, 500, "读取 agents 失败")
			return
		}
		for i := range agents {
			if agents[i].Error != "" {
				agents[i].Error = i18n.Translate(w.Header().Get("Content-Language"), "Model discovery failed. The last successful list is retained. Contact your administrator.")
			}
		}
		apiJSON(w, 200, agents)
	case path == "/api/presence" && r.Method == "POST":
		var input struct {
			ClientID string `json:"client_id"`
			Visible  bool   `json:"visible"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if !chatmodel.ValidID(input.ClientID) {
			apiError(w, 400, "invalid client ID")
			return
		}
		a.presence.Touch(id, input.ClientID, input.Visible)
		apiJSON(w, 200, a.presence.List(a.store))
	case path == "/api/presence/events" && r.Method == "GET":
		a.presenceStream(w, r)
	case path == "/api/chats" && r.Method == "GET":
		sessions, err := a.store.UserSessions(id.User.ID, r.URL.Query().Get("project_id"))
		if err != nil {
			apiError(w, 403, "无权访问该项目")
			return
		}
		for i := range sessions {
			sessions[i].NativeSessionID = ""
		}
		apiJSON(w, 200, sessions)
	case path == "/api/chats" && r.Method == "POST":
		var input struct {
			ProjectID string            `json:"project_id"`
			Backend   chatmodel.Backend `json:"backend"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.Backend == "" {
			input.Backend = chatmodel.BackendCodex
		}
		chat, err := a.store.CreateChat(id.User.ID, input.ProjectID, input.Backend)
		if err != nil {
			apiError(w, 403, "无权在该项目创建聊天")
			return
		}
		apiJSON(w, 201, chat)
	case strings.HasPrefix(path, "/api/chats/"):
		a.chatRoute(w, r, id, path)
	default:
		apiError(w, 404, "接口不存在")
	}
}

func (a *App) signedInResponse(w http.ResponseWriter, id authIdentity) {
	preferences, err := a.store.UIPreferences(id.User.ID)
	if err != nil {
		apiError(w, 500, "Could not save preferences")
		return
	}
	apiJSON(w, 200, map[string]any{"user": id.User, "csrf": id.CSRF, "preferences": preferences})
}
func (a *App) enabled(backend chatmodel.Backend) bool {
	all, err := a.store.Agents()
	if err != nil {
		return false
	}
	for _, v := range all {
		if v.Backend == backend {
			return v.Enabled && v.Installed
		}
	}
	return false
}
func (a *App) accessibleChat(user, id string) (chatmodel.Session, bool) {
	chat, ok := a.store.Get(id)
	return chat, ok && a.store.CanAccessChat(user, id)
}
func (a *App) snapshot(chat chatmodel.Session) chatmodel.Session {
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	if t, ok := a.live[chat.ID]; ok && chat.Status == chatmodel.StatusRunning && len(chat.Turns) > 0 && chat.Turns[len(chat.Turns)-1].ID == t.ID {
		base := chat.Transcript
		if hydrated, exists := a.liveBase[chat.ID]; exists {
			base = hydrated
		}
		chat.Transcript = runningTranscript(base, t)
		// Metadata and the in-memory stream can briefly have different
		// transcript bases (for example after a reconnect). Clamp the live
		// offset before slicing so a malformed/stale offset cannot panic HTTP.
		start := max(0, min(t.PartStart, len(chat.Transcript.Parts)))
		t.PartStart = start
		t.InputEnd = max(start, min(t.InputEnd, len(chat.Transcript.Parts)))
		t.Parts = append([]chatmodel.Part(nil), chat.Transcript.Parts[start:]...)
		t.PartEnd = t.PartStart + len(t.Parts)
		chat.Turns[len(chat.Turns)-1] = t
		chat.Status = t.Status
	} else if finished, exists := a.completed[chat.ID]; exists {
		chat, _ = mergeCompletedTurn(chat, finished)
	}
	chat.NativeSessionID = ""
	if len(chat.Turns) > 0 {
		chat.PermissionMode = chat.Turns[len(chat.Turns)-1].PermissionMode
	}
	return chat
}

// mergeCompletedTurn bridges the short interval between a provider finishing
// and its native session file containing the final assistant message. The
// response stays in memory only; once native history catches up, callers use
// that source of truth instead.
func mergeCompletedTurn(chat chatmodel.Session, finished completedTurn) (chatmodel.Session, bool) {
	if len(chat.Turns) == 0 || chat.Turns[len(chat.Turns)-1].ID != finished.turn.ID {
		return chat, false
	}
	candidate := runningTranscript(finished.base, finished.turn)
	if len(candidate.Parts) <= len(chat.Transcript.Parts) {
		return chat, false
	}
	chat.Transcript = candidate
	turn := finished.turn
	turn.PartStart = len(finished.base.Parts)
	turn.Parts = append([]chatmodel.Part(nil), candidate.Parts[turn.PartStart:]...)
	turn.PartEnd = turn.PartStart + len(turn.Parts)
	chat.Turns[len(chat.Turns)-1] = turn
	chat.Status = turn.Status
	return chat, true
}
func (a *App) chatRoute(w http.ResponseWriter, r *http.Request, id authIdentity, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/chats/"), "/")
	if len(parts) > 2 || !chatmodel.ValidID(parts[0]) {
		apiError(w, 404, "聊天不存在")
		return
	}
	chat, ok := a.accessibleChat(id.User.ID, parts[0])
	if !ok {
		apiError(w, 404, "聊天不存在")
		return
	}
	if len(parts) == 1 && r.Method == "GET" {
		apiJSON(w, 200, a.snapshot(a.historicalSnapshot(chat)))
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == "GET" {
		a.chatStream(w, r, id.User.ID, chat.ID)
		return
	}
	if len(parts) == 2 && parts[1] == "attachments" && (r.Method == "GET" || r.Method == "HEAD") {
		a.attachmentResponse(w, r, chat)
		return
	}
	// Shared project members can inspect another person's chat, but only its
	// owner may send messages or control the agent run.
	if chat.UserID != id.User.ID {
		apiError(w, 404, "聊天不存在")
		return
	}
	if len(parts) == 2 && parts[1] == "approvals" && r.Method == "POST" {
		a.approvalResponse(w, r, chat)
		return
	}
	if len(parts) == 2 && parts[1] == "stop" && r.Method == "POST" {
		a.stopTurnResponse(w, r, chat)
		return
	}
	if len(parts) != 2 || parts[1] != "messages" || r.Method != "POST" {
		apiError(w, 404, "接口不存在")
		return
	}
	input, valid := readMessageInput(w, r)
	if !valid {
		return
	}
	if input.Backend != "" {
		if len(chat.Turns) > 0 && input.Backend != chat.Backend {
			apiError(w, 409, errChatAgentLocked.Error())
			return
		}
		chat.Backend = input.Backend
	}
	if !a.enabled(chat.Backend) {
		apiError(w, 409, "agent 未启用或尚未安装")
		return
	}
	options := chatmodel.CodexOptions{Model: input.Model, ReasoningEffort: input.Effort}
	if err := a.validateSelection(r.Context(), chat, options); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := chatmodel.ValidatePermissionMode(chat.Backend, input.PermissionMode); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if input.PermissionMode != "" {
		catalog, err := a.permissionCatalog(r.Context(), chat)
		if err != nil {
			apiError(w, 503, "Permission discovery is unavailable")
			return
		}
		if err := catalog.Validate(input.PermissionMode); err != nil {
			apiError(w, 400, err.Error())
			return
		}
	}
	if err := a.startTurnWithInput(chat, input, options); err != nil {
		status := 400
		if strings.Contains(err.Error(), "running turn") || errors.Is(err, errChatAgentLocked) {
			status = 409
		}
		if errors.Is(err, ErrUpdateInProgress) {
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "30")
		}
		apiError(w, status, err.Error())
		return
	}
	latest, _ := a.store.Get(chat.ID)
	apiJSON(w, 202, a.snapshot(latest))
}
func (a *App) validateSelection(ctx context.Context, chat chatmodel.Session, options chatmodel.CodexOptions) error {
	if err := chatmodel.ValidateCodexOptions(options); err != nil {
		return err
	}
	if chat.Backend == chatmodel.BackendClaude {
		if err := chatmodel.ValidateClaudeEffort(options.ReasoningEffort); err != nil {
			return err
		}
		if options.ReasoningEffort == "" {
			return nil
		}
	}
	all, err := a.store.Agents()
	if err != nil {
		return err
	}
	for _, v := range all {
		if v.Backend == chat.Backend && len(v.Catalog.Models) > 0 {
			if chat.Backend == chatmodel.BackendClaude {
				model := options.Model
				if model == "" {
					model = v.Catalog.DefaultModel
					if model == "" {
						model = "default"
					}
				}
				entry, known := v.Catalog.Find(model)
				if !known || len(entry.Efforts) == 0 {
					return fmt.Errorf("This model has no adjustable reasoning level.")
				}
				options.Model = model
			}
			return v.Catalog.Validate(options)
		}
	}
	if chat.Backend == chatmodel.BackendClaude {
		return fmt.Errorf("This model has no adjustable reasoning level.")
	}
	return nil
}
func (a *App) startTurn(chat chatmodel.Session, prompt string, options chatmodel.CodexOptions) error {
	return a.startTurnWithInput(chat, messageInput{Message: prompt}, options)
}
func (a *App) startTurnWithInput(chat chatmodel.Session, input messageInput, options chatmodel.CodexOptions) error {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closing {
		return fmt.Errorf("agent service is stopping")
	}
	if a.updating {
		return ErrUpdateInProgress
	}
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		return err
	}
	if _, err = agent.WorkingDirectory(project.Path); err != nil {
		return fmt.Errorf("project directory is unavailable")
	}
	turn, err := a.store.beginTurnWithInput(chat.ID, input.Message, chat.Backend, input.PermissionMode, input.files, options)
	if err != nil {
		return err
	}
	// beginTurnWithInput reads SQLite, which intentionally has no transcript
	// body. Restore the hydrated history offset for the in-memory stream so a
	// resumed chat can append deltas instead of replacing the whole transcript.
	base := cloneTranscript(chat.Transcript)
	inputParts := max(0, turn.InputEnd-turn.PartStart)
	turn.PartStart = len(base.Parts)
	turn.InputEnd = turn.PartStart + inputParts
	turn.PartEnd = turn.PartStart + len(turn.Parts)
	a.liveMu.Lock()
	delete(a.completed, chat.ID)
	if a.streams[chat.ID] > 0 {
		a.streamSeen[chat.ID] = true
	}
	a.liveBase[chat.ID] = base
	a.live[chat.ID] = turn
	a.liveMu.Unlock()
	ctx, control := a.registerTurn(context.Background(), chat.ID, turn.ID)
	a.turns.Add(1)
	go func() {
		defer a.turns.Done()
		defer a.releaseTurn(chat.ID, control)
		if _, err := a.executeTurn(ctx, chat.ID, input.Message, turn, nil); err != nil {
			log.Printf("chat %s: %v", chat.ID, err)
		}
	}()
	return nil
}

// runTurn is the synchronous core used by process lifecycle tests and local callers.
func (a *App) runTurn(ctx context.Context, id, prompt string, emit func(agent.AgentEvent) error, options ...chatmodel.CodexOptions) (agent.AgentResult, error) {
	a.turnMu.Lock()
	if a.closing {
		a.turnMu.Unlock()
		return agent.AgentResult{}, fmt.Errorf("agent service is stopping")
	}
	if a.updating {
		a.turnMu.Unlock()
		return agent.AgentResult{}, ErrUpdateInProgress
	}
	turn, err := a.store.BeginTurn(id, prompt, options...)
	if err != nil {
		a.turnMu.Unlock()
		return agent.AgentResult{}, err
	}
	ctx, control := a.registerTurn(ctx, id, turn.ID)
	a.turns.Add(1)
	a.turnMu.Unlock()
	defer a.turns.Done()
	defer a.releaseTurn(id, control)
	return a.executeTurn(ctx, id, prompt, turn, emit)
}
func (a *App) executeTurn(ctx context.Context, id, prompt string, turn chatmodel.Turn, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	chat, _ := a.store.Get(id)
	finish := func(result agent.AgentResult, runErr error) (agent.AgentResult, error) {
		a.liveMu.Lock()
		if current, ok := a.live[id]; ok && current.ID == turn.ID {
			inputCount := max(0, min(current.InputEnd-current.PartStart, len(current.Parts)))
			if len(current.Parts) > inputCount {
				result.Parts = append([]chatmodel.Part(nil), current.Parts[inputCount:]...)
			}
		}
		a.liveMu.Unlock()
		err := a.store.FinishTurn(id, turn.ID, result, runErr, statusForAgentError(runErr))
		a.liveMu.Lock()
		if err == nil {
			terminal := turn
			if current, ok := a.live[id]; ok && current.ID == turn.ID {
				terminal = current
			}
			terminal.Status = statusForAgentError(runErr)
			terminal.FinishedAt = time.Now().UnixMilli()
			if runErr != nil {
				terminal.Error = chatmodel.Clip(runErr.Error(), 8192)
			}
			if result.Text != "" {
				missing := result.Text
				if strings.HasPrefix(result.Text, terminal.Reply) {
					missing = result.Text[len(terminal.Reply):]
				} else if result.Text == terminal.Reply {
					missing = ""
				}
				if missing != "" {
					appendTurnText(&terminal, chatmodel.Part{Role: "assistant", Kind: "text", Model: result.Model, Text: missing})
				}
				terminal.Reply = result.Text
			}
			if a.streamSeen[id] {
				a.completed[id] = completedTurn{turn: terminal, base: cloneTranscript(a.liveBase[id])}
			}
			delete(a.streamSeen, id)
			delete(a.live, id)
			delete(a.liveBase, id)
			finishedID := terminal.ID
			finishedAt := terminal.FinishedAt
			time.AfterFunc(30*time.Second, func() {
				a.liveMu.Lock()
				if value, ok := a.completed[id]; ok && value.turn.ID == finishedID && value.turn.FinishedAt == finishedAt {
					delete(a.completed, id)
				}
				a.liveMu.Unlock()
			})
		} else if errors.Is(err, errTurnChanged) {
			// A stopped turn may finish after the user has already submitted a
			// follow-up turn. Never replace that newer live turn with the old
			// provider result or an obsolete failure marker.
		} else {
			failed := turn
			failed.Reply, failed.Model, failed.Activities = result.Text, result.Model, result.Activities
			failed.Status, failed.FinishedAt, failed.Error = chatmodel.StatusFailed, time.Now().UnixMilli(), "Could not persist this response: "+err.Error()
			a.live[id] = failed
		}
		a.liveMu.Unlock()
		if err != nil {
			return result, fmt.Errorf("could not save response: %w", err)
		}
		return result, runErr
	}
	binary, err := a.runner.Binary(chat.Backend)
	if err != nil {
		return finish(agent.AgentResult{}, err)
	}
	directory := ""
	if chat.ProjectID != "" {
		project, e := a.store.Project(chat.ProjectID)
		if e != nil {
			return finish(agent.AgentResult{}, e)
		}
		directory = project.Path
	}
	request := agent.AgentRequest{ChatID: id, Backend: chat.Backend, Binary: binary, WorkingDirectory: directory, NativeSessionID: chat.NativeSessionID, Prompt: prompt, Model: turn.RequestedModel, ReasoningEffort: turn.ReasoningEffort}
	request.PermissionMode = turn.PermissionMode
	request.Files, err = a.materializeAttachments(chat)
	if err != nil {
		return finish(agent.AgentResult{}, err)
	}
	for _, file := range request.Files {
		for _, current := range turn.Attachments {
			if current.ID == file.ID {
				request.Attachments = append(request.Attachments, file)
				break
			}
		}
	}
	request.Approve = func(ctx context.Context, request chatmodel.ApprovalRequest) (bool, error) {
		return a.requestApproval(ctx, id, turn.ID, request)
	}
	lastSave := time.Now()
	result, runErr := a.runner.Run(ctx, request, func(event agent.AgentEvent) error {
		if event.Type == "session" {
			if err := a.store.SetNativeSession(id, event.SessionID); err != nil {
				return err
			}
		}
		a.liveMu.Lock()
		current, ok := a.live[id]
		if !ok {
			current = turn
		}
		switch event.Type {
		case "delta":
			current.Reply = chatmodel.AppendBounded(current.Reply, event.Text, chatmodel.MaxReplyBytes)
			appendTurnText(&current, chatmodel.Part{Role: "assistant", Kind: "text", Model: current.Model, Text: event.Text})
		case "part":
			if event.Part != nil {
				if event.Part.Kind == "thinking" {
					appendTurnText(&current, *event.Part)
				} else {
					appendTurnPart(&current, *event.Part)
				}
			}
		case "model":
			current.Model = event.Model
			current.ReasoningEffort = event.ReasoningEffort
			// A provider may announce the model after the first reasoning or
			// tool event. Backfill every assistant part from this turn so live
			// parts have the same model metadata as the native transcript.
			for i := range current.Parts {
				if current.Parts[i].Role == "assistant" && current.Parts[i].Model == "" {
					current.Parts[i].Model = event.Model
				}
			}
		case "activity":
			current.Activities = boundedActivities(append(current.Activities, event.Text))
			part := chatmodel.Part{Role: "assistant", Kind: "context", Model: current.Model, Text: event.Text}
			if event.Part != nil {
				part = *event.Part
			}
			appendTurnPart(&current, part)
		}
		current.PartEnd = current.PartStart + len(current.Parts)
		a.live[id] = current
		persistTurn := current
		persistTurn.Parts = append([]chatmodel.Part(nil), current.Parts...)
		persistTurn.Activities = append([]string(nil), current.Activities...)
		a.liveMu.Unlock()
		if time.Since(lastSave) > time.Second {
			if err := a.store.SaveRunningTurn(id, persistTurn); err != nil {
				return err
			}
			lastSave = time.Now()
		}
		if emit != nil {
			return emit(event)
		}
		return nil
	})
	return finish(result, runErr)
}
func (a *App) Close() error {
	a.closeOnce.Do(func() { close(a.shutdown) })
	a.turnMu.Lock()
	a.closing = true
	for _, control := range a.activeTurns {
		control.cancel()
	}
	a.turnMu.Unlock()
	var err error
	if closer, ok := a.runner.(interface{ Close() error }); ok {
		err = closer.Close()
	}
	a.turns.Wait()
	a.filesMu.Lock()
	if a.filesRoot != "" {
		_ = os.RemoveAll(a.filesRoot)
		a.filesRoot = ""
	}
	a.filesMu.Unlock()
	return err
}
func statusForAgentError(err error) chatmodel.SessionStatus {
	if err == nil {
		return chatmodel.StatusCompleted
	}
	if errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "agent stopped") {
		return chatmodel.StatusStopped
	}
	return chatmodel.StatusFailed
}
func startSSE(w http.ResponseWriter) bool {
	if _, ok := w.(http.Flusher); !ok {
		apiError(w, 500, "streaming unavailable")
		return false
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	return true
}
func writeSSE(w http.ResponseWriter, event string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	return controller.Flush()
}
func (a *App) chatStream(w http.ResponseWriter, r *http.Request, user, id string) {
	chat, ok := a.accessibleChat(user, id)
	if !ok {
		apiError(w, 404, "聊天不存在")
		return
	}
	if !startSSE(w) {
		return
	}
	a.liveMu.Lock()
	a.streams[id]++
	a.liveMu.Unlock()
	defer func() {
		a.liveMu.Lock()
		if a.streams[id] <= 1 {
			delete(a.streams, id)
		} else {
			a.streams[id]--
		}
		a.liveMu.Unlock()
	}()
	chat = a.snapshot(a.historicalSnapshot(chat))
	if err := writeSSE(w, "snapshot", chat); err != nil {
		return
	}
	transcriptSnapshot := chat.Transcript
	baseTranscript := chat.Transcript
	a.liveMu.Lock()
	if hydrated, exists := a.liveBase[id]; exists {
		baseTranscript = hydrated
	}
	a.liveMu.Unlock()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	heart := time.NewTicker(a.streamHeartbeat)
	defer heart.Stop()
	var previous string
	var cached chatmodel.Turn
	var cachedUpdated int64 = -1
	for {
		// Poll only indexed metadata. Never decode an entire conversation on every
		// streaming frame; live replies are already available in memory.
		meta, err := scanSession(a.store.db.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id=?`, id))
		if err != nil || !a.store.CanAccessChat(user, id) {
			return
		}
		a.liveMu.Lock()
		current, live := a.live[id]
		a.liveMu.Unlock()
		if !live || meta.Status != chatmodel.StatusRunning {
			if cachedUpdated != meta.UpdatedAt {
				latest, ok := a.store.Get(id)
				if ok {
					latest = a.historicalSnapshot(latest)
					a.liveMu.Lock()
					if finished, exists := a.completed[id]; exists {
						var used bool
						latest, used = mergeCompletedTurn(latest, finished)
						if !used {
							delete(a.completed, id)
						}
					}
					a.liveMu.Unlock()
					transcriptSnapshot = latest.Transcript
					baseTranscript = latest.Transcript
					if len(latest.Turns) > 0 {
						cached = latest.Turns[len(latest.Turns)-1]
					} else {
						cached = chatmodel.Turn{}
					}
				} else {
					cached = chatmodel.Turn{}
				}
				cachedUpdated = meta.UpdatedAt
			}
			current = cached
		} else {
			if cachedUpdated != meta.UpdatedAt {
				if latest, ok := a.store.Get(id); ok {
					// A running SQLite snapshot intentionally has no transcript
					// body. Keep the hydrated base captured before this turn rather
					// than replacing it with an empty transcript on every poll.
					if len(latest.Transcript.Parts) > 0 {
						baseTranscript = latest.Transcript
					}
				}
				cachedUpdated = meta.UpdatedAt
			}
			transcriptSnapshot = runningTranscript(baseTranscript, current)
		}
		if current.ID != "" {
			status := meta.Status
			if live && meta.Status == chatmodel.StatusRunning {
				status = current.Status
			}
			approval := ""
			if current.PendingApproval != nil {
				approval = current.PendingApproval.ID
			}
			partSignature, _ := json.Marshal(transcriptSnapshot.Parts)
			signature := fmt.Sprintf("%s/%s/%s/%s/%x/%d/%s/%s", current.ID, status, current.Model, current.ReasoningEffort, partSignature, current.FinishedAt, current.Error, approval)
			if signature != previous {
				value := struct {
					Turn       chatmodel.Turn          `json:"turn"`
					Transcript chatmodel.Transcript    `json:"transcript"`
					Status     chatmodel.SessionStatus `json:"status"`
					Title      string                  `json:"title"`
					UpdatedAt  int64                   `json:"updated_at"`
					Backend    chatmodel.Backend       `json:"backend"`
				}{current, transcriptSnapshot, status, meta.Title, meta.UpdatedAt, meta.Backend}
				if err := writeSSE(w, "turn", value); err != nil {
					return
				}
				previous = signature
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		case <-heart.C:
			if _, err := a.identity(r); err != nil {
				return
			}
			if err := writeSSE(w, "ping", map[string]bool{"ok": true}); err != nil {
				return
			}
		}
	}
}
func (a *App) presenceStream(w http.ResponseWriter, r *http.Request) {
	if !startSSE(w) {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := a.identity(r); err != nil {
			return
		}
		if err := writeSSE(w, "presence", a.presence.List(a.store)); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
