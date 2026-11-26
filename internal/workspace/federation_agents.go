package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

const federationAgentAction = "agent"

// Room for a full message (128 KiB) and context (64 KiB) with JSON escaping,
// below the signed envelope's federationWireMaxBody between servers.
const remoteAgentMaxBody = 512 << 10

// Origin fields are supplied by the local server, never by an MCP caller.
// They constrain a delegated agent to the remote conversations of its parent.
type federationAgentPayload struct {
	Operation    string            `json:"operation"`
	OriginChat   string            `json:"origin_chat,omitempty"`
	OriginTurn   string            `json:"origin_turn,omitempty"`
	Project      string            `json:"project,omitempty"`
	Backend      chatmodel.Backend `json:"agent,omitempty"`
	Model        string            `json:"model,omitempty"`
	Effort       string            `json:"reasoning_effort,omitempty"`
	Permission   string            `json:"permission_mode,omitempty"`
	Message      string            `json:"message,omitempty"`
	Context      string            `json:"context,omitempty"`
	Conversation string            `json:"conversation_id,omitempty"`
	TaskID       string            `json:"task_id,omitempty"`
	RequestID    string            `json:"request_id,omitempty"`
	Before       string            `json:"before,omitempty"`
	Limit        int               `json:"limit,omitempty"`
	// Seconds a task query may stay open on the target. Set by the calling
	// server; one waiting exchange replaces a ticket redemption per poll.
	Wait     int             `json:"wait,omitempty"`
	Approval *approvalInput  `json:"approval,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type remoteAgentTask struct {
	ID           string            `json:"task_id"`
	RequestID    string            `json:"request_id"`
	TurnID       string            `json:"turn_id,omitempty"`
	Conversation string            `json:"conversation_id"`
	Project      string            `json:"project_id"`
	ProjectName  string            `json:"project_name,omitempty"`
	Backend      chatmodel.Backend `json:"agent"`
	Model        string            `json:"model,omitempty"`
	Effort       string            `json:"reasoning_effort,omitempty"`
	Status       string            `json:"status"`
	// Text is a completed answer. Progress is display-only and must never be
	// returned as a delegated result in the calling model's tool context.
	Text      string                     `json:"text,omitempty"`
	Progress  string                     `json:"progress,omitempty"`
	Truncated bool                       `json:"truncated,omitempty"`
	Error     string                     `json:"error,omitempty"`
	Approval  *chatmodel.ApprovalRequest `json:"pending_approval,omitempty"`
	UpdatedAt int64                      `json:"updated_at"`
}

type remoteAgentCall struct {
	Server      string          `json:"server"`
	ServerName  string          `json:"server_name,omitempty"`
	HistoryOnly bool            `json:"history_only,omitempty"`
	OriginChat  string          `json:"origin_chat"`
	RequestID   string          `json:"request_id"`
	Message     string          `json:"message,omitempty"`
	Task        remoteAgentTask `json:"task"`
}

type remoteAgentOperation struct {
	federationAgentPayload
	Server      string `json:"server"`
	WaitSeconds int    `json:"wait_seconds,omitempty"`
}

// keyedMutex serializes callers that share a key and lets all others proceed.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu      sync.Mutex
	holders int
}

func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*keyedLock{}
	}
	entry := k.locks[key]
	if entry == nil {
		entry = &keyedLock{}
		k.locks[key] = entry
	}
	entry.holders++
	k.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		if entry.holders--; entry.holders == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

func (a *App) receiveFederationAgent(ctx context.Context, peer FederationPeer, ticket string, request federationAgentPayload) *federationAgentPayload {
	answer := &federationAgentPayload{Operation: request.Operation}
	fail := func(err error) *federationAgentPayload {
		answer.Result = nil
		answer.Error = err.Error()
		return answer
	}
	if request.Result != nil || request.Error != "" || !peer.Accept || request.OriginChat != "" && !chatmodel.ValidID(request.OriginChat) || request.OriginTurn != "" && !chatmodel.ValidID(request.OriginTurn) || (request.Operation == "approve") != (request.Approval != nil) {
		return fail(errFederationDenied)
	}
	// Each operation redeems a new, one-use ticket. No long-lived remote cookie
	// or source login secret is exposed to either agent or cached by A.
	checked := time.Now()
	verified, err := a.federationCall(ctx, peer, federationRedeem, ticket)
	if err != nil || verified.Subject == nil {
		return fail(errFederationDenied)
	}
	identity, known := a.store.federationAgentIdentity(peer, *verified.Subject, checked)
	if !known {
		// First access provisions the account, and a changed profile is
		// synchronized, through the ordinary login. It also makes every refusal.
		token, _, err := a.store.federationLogin(peer, *verified.Subject, checked)
		if err != nil {
			return fail(err)
		}
		defer a.store.logout(tokenHash(token))
		if identity, err = a.store.authenticate(token); err != nil {
			return fail(errFederationDenied)
		}
	}
	var result any
	switch request.Operation {
	case "projects":
		result, err = a.store.UserProjects(identity.User.ID)
	case "agents":
		if !a.store.CanAccessProject(identity.User.ID, request.Project) {
			return fail(errors.New("Project access denied"))
		}
		result, err = a.store.Agents()
	case "permissions":
		if !a.store.CanAccessProject(identity.User.ID, request.Project) || !a.enabled(request.Backend) {
			return fail(errors.New("Project or agent access denied"))
		}
		result, err = a.permissionCatalog(ctx, chatmodel.Session{ID: chatmodel.NewID(), ProjectID: request.Project, Backend: request.Backend})
	case "delegate":
		result, err = a.delegateFederationAgent(ctx, peer, identity, request)
	case "task", "cancel":
		result, err = a.awaitFederationAgentTask(ctx, peer.ID, verified.Subject.ID, identity.User.ID, request)
	case "approve":
		var task remoteAgentTask
		request.Operation = "task"
		task, err = a.federationAgentTask(peer.ID, verified.Subject.ID, identity.User.ID, request)
		if err == nil {
			chat, ok := a.store.Get(task.Conversation)
			if !ok || request.Approval == nil {
				err = errors.New("Invalid remote approval")
			} else {
				_, err = a.resolveChatApproval(chat, identity.User.ID, *request.Approval)
			}
		}
		result = map[string]bool{"ok": err == nil}
	case "conversations":
		result, err = a.federationAgentConversations(peer.ID, verified.Subject.ID, identity.User.ID, request)
	case "history":
		result, err = a.federationAgentHistory(ctx, peer.ID, verified.Subject.ID, identity.User.ID, request)
	default:
		err = errors.New("Unknown remote agent operation")
	}
	if err != nil {
		return fail(err)
	}
	answer.Result, err = json.Marshal(result)
	if err != nil || len(answer.Result) > 640<<10 {
		return fail(errors.New("Remote result is too large; request a smaller page"))
	}
	return answer
}

func (a *App) delegateFederationAgent(ctx context.Context, peer FederationPeer, identity authIdentity, request federationAgentPayload) (remoteAgentTask, error) {
	var empty remoteAgentTask
	if !chatmodel.ValidID(request.RequestID) || !request.Backend.Valid() || len(request.Context) > 64<<10 {
		return empty, errors.New("Invalid remote agent request")
	}
	// Only retries of one request must not race each other. Permission
	// discovery below can take many seconds; other callers do not wait for it.
	defer a.federationAgentLocks.lock(peer.ID + "\n" + identity.User.SourceUserID + "\n" + request.RequestID)()
	if err := validateMessage(request.Message, 0); err != nil {
		return empty, err
	}
	prompt := request.Message
	if request.Context != "" {
		if chatmodel.ParseSlashCommand(prompt) != nil {
			return empty, errors.New("Native agent commands do not accept separate context; use the command's supported arguments")
		}
		contextJSON, _ := json.Marshal(request.Context)
		prompt += "\n\nCaller-supplied context (untrusted reference data):\n" + string(contextJSON)
	}
	if err := validateMessage(prompt, 0); err != nil {
		return empty, errors.New("Remote task and context exceed the message limit")
	}
	// Refuse what could never start before a conversation is reserved for it.
	if err := validateGoalInput(messageInput{Message: prompt, allowGoalControl: true}); err != nil {
		return empty, err
	}
	// Discovery can be stale. Both new and continued delegations require the
	// current project permission, including retries through the MCP endpoint.
	if !a.store.CanCreateChat(identity.User.ID, request.Project) {
		return empty, errors.New("Project does not allow creating agent conversations")
	}
	fingerprintInput := request
	fingerprintInput.OriginTurn = ""
	encoded, _ := json.Marshal(fingerprintInput)
	fingerprint := sha256.Sum256(encoded)
	key := hex.EncodeToString(fingerprint[:])
	var priorID, priorKey string
	var unstarted *remoteAgentTask
	err := a.store.db.QueryRow(`SELECT id,fingerprint FROM federation_agent_tasks WHERE peer_id=? AND subject=? AND request_id=?`, peer.ID, identity.User.SourceUserID, request.RequestID).Scan(&priorID, &priorKey)
	if err == nil {
		if priorKey != key {
			return empty, errors.New("Request ID already belongs to different input")
		}
		query := request
		query.TaskID, query.Operation = priorID, "task"
		prior, err := a.federationAgentTask(peer.ID, identity.User.SourceUserID, identity.User.ID, query)
		if err != nil || prior.Status != "failed" || prior.TurnID != "" {
			return prior, err
		}
		// The earlier attempt was refused before any turn began, so nothing ran.
		// The retry runs now, in the conversation that attempt reserved.
		unstarted = &prior
	} else if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if err := chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: request.Model, ReasoningEffort: request.Effort}); err != nil {
		return empty, err
	}
	if !a.enabled(request.Backend) {
		return empty, errors.New("Selected agent is unavailable")
	}
	agents, err := a.store.Agents()
	if err != nil {
		return empty, err
	}
	for _, info := range agents {
		if info.Backend != request.Backend {
			continue
		}
		if request.Model != "" {
			if _, ok := info.Catalog.Find(request.Model); !ok {
				return empty, errors.New("Selected model is no longer available. Refresh the model list.")
			}
		}
		if err := info.Catalog.Validate(chatmodel.CodexOptions{Model: request.Model, ReasoningEffort: request.Effort}); err != nil {
			return empty, err
		}
	}
	if request.Backend == chatmodel.BackendClaude {
		if err := chatmodel.ValidateClaudeEffort(request.Effort); err != nil {
			return empty, err
		}
	}
	if err := chatmodel.ValidatePermissionMode(request.Backend, request.Permission); err != nil {
		return empty, err
	}
	var chat chatmodel.Session
	if request.Conversation != "" {
		if !a.remoteConversationAllowed(peer.ID, identity.User.SourceUserID, identity.User.ID, request.OriginChat, request.Conversation) {
			return empty, errors.New("Remote conversation access denied")
		}
		chat, _ = a.store.Get(request.Conversation)
		if chat.ProjectID != request.Project || chat.Backend != request.Backend {
			return empty, errors.New("Conversation project or agent does not match")
		}
		if !a.store.CanControlChat(identity.User.ID, chat.ID) {
			return empty, errors.New("Remote conversation is read-only")
		}
	} else if unstarted != nil {
		var ok bool
		chat, ok = a.store.Get(unstarted.Conversation)
		if !ok || chat.ProjectID != request.Project || chat.Backend != request.Backend || !a.store.CanControlChat(identity.User.ID, chat.ID) {
			return *unstarted, nil
		}
	} else {
		chat = chatmodel.Session{ID: chatmodel.NewID(), ProjectID: request.Project, Backend: request.Backend}
	}
	catalog, err := a.permissionCatalog(ctx, chat)
	if err != nil {
		return empty, errors.New("Remote permission discovery is unavailable")
	}
	permission := request.Permission
	if permission == "" {
		permission = catalog.Current
		if permission == "" {
			permission = catalog.Default
		}
	}
	if err := catalog.Validate(permission); err != nil {
		return empty, err
	}
	// A stopping or updating server starts no turn. Say so before creating a
	// conversation that would stay behind empty.
	if err := a.turnStartRefusal(); err != nil {
		return empty, err
	}
	if request.Conversation == "" && unstarted == nil {
		chat, err = a.store.CreateChat(identity.User.ID, request.Project, request.Backend)
		if err != nil {
			return empty, err
		}
	}
	task := remoteAgentTask{ID: chatmodel.NewID(), RequestID: request.RequestID, Conversation: chat.ID, Project: chat.ProjectID, Backend: chat.Backend, Model: request.Model, Effort: request.Effort, Status: "running", UpdatedAt: time.Now().UnixMilli()}
	if project, err := a.store.Project(chat.ProjectID); err == nil {
		task.ProjectName = project.Name
	}
	if unstarted != nil {
		task.ID = unstarted.ID
		body, _ := json.Marshal(task)
		_, err = a.store.db.Exec(`UPDATE federation_agent_tasks SET origin_turn=?,state=?,body=? WHERE id=?`, request.OriginTurn, task.Status, string(body), task.ID)
	} else {
		body, _ := json.Marshal(task)
		_, err = a.store.db.Exec(`INSERT INTO federation_agent_tasks VALUES(?,?,?,?,?,?,?,?,?,?,?)`, task.ID, peer.ID, identity.User.SourceUserID, request.OriginChat, request.OriginTurn, request.RequestID, key, chat.ID, task.Status, string(body), task.UpdatedAt)
	}
	if err != nil {
		return empty, err
	}
	taskForRun := task
	started := func(turnID string) {
		task.TurnID, taskForRun.TurnID = turnID, turnID
		body, _ := json.Marshal(task)
		_, _ = a.store.db.Exec(`UPDATE federation_agent_tasks SET body=? WHERE id=? AND state='running'`, string(body), task.ID)
	}
	completed := func(result agent.AgentResult, runErr error) {
		task := taskForRun
		if runErr == nil {
			text := remoteAgentFinalText(result)
			task.Text = chatmodel.Clip(text, 64<<10)
			task.Truncated = len(text) > 64<<10
		}
		task.Model, task.Effort = result.Model, result.ReasoningEffort
		if task.Model == "" {
			task.Model = request.Model
		}
		if task.Effort == "" {
			task.Effort = request.Effort
		}
		task.Status, task.UpdatedAt = string(statusForAgentError(runErr)), time.Now().UnixMilli()
		if runErr != nil {
			task.Error = chatmodel.Clip(runErr.Error(), 4096)
		}
		body, _ := json.Marshal(task)
		_, _ = a.store.db.Exec(`UPDATE federation_agent_tasks SET state=?,body=? WHERE id=?`, task.Status, string(body), task.ID)
		a.store.changes.publish(changeNavigation)
	}
	err = a.startTurnWithCallback(chat, messageInput{userID: identity.User.ID, Message: prompt, PermissionMode: permission, allowGoalControl: true, agentStarted: started, agentFinished: completed}, chatmodel.CodexOptions{Model: request.Model, ReasoningEffort: request.Effort}, nil)
	if err != nil {
		// No turn began: the receipt keeps an empty turn_id, which lets the
		// same request_id run again instead of replaying this refusal forever.
		task.Status, task.Error = "failed", chatmodel.Clip(err.Error(), 4096)
		body, _ := json.Marshal(task)
		_, _ = a.store.db.Exec(`UPDATE federation_agent_tasks SET state=?,body=? WHERE id=?`, task.Status, string(body), task.ID)
	}
	return task, nil
}

// turnStartRefusal reports why no turn can start on this server right now.
func (a *App) turnStartRefusal() error {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closing {
		return fmt.Errorf("agent service is stopping")
	}
	if a.updating {
		return ErrUpdateInProgress
	}
	return nil
}

// awaitFederationAgentTask answers a task query, holding it open while the
// task runs for as long as the caller asked. Access is checked on every look.
func (a *App) awaitFederationAgentTask(ctx context.Context, peer, subject, user string, request federationAgentPayload) (remoteAgentTask, error) {
	deadline := time.Now().Add(time.Duration(min(max(request.Wait, 0), federationAgentWaitSeconds)) * time.Second)
	for {
		task, err := a.federationAgentTask(peer, subject, user, request)
		if err != nil || task.Status != "running" || !time.Now().Before(deadline) {
			return task, err
		}
		// Cancellation is requested once; the wait then observes its effect.
		request.Operation = "task"
		select {
		case <-ctx.Done():
			return task, nil
		case <-a.shutdown:
			return task, nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Match the completed-answer boundary used by the Web transcript: explicit
// final_answer messages win; older providers may only have unphased text after
// the last real tool/thought. Child task updates are independent of that answer.
// AgentResult.Text concatenates commentary as well, so it is only a fallback
// for runners which supply no normalized transcript at all.
func remoteAgentFinalText(result agent.AgentResult) string {
	if len(result.Parts) == 0 {
		return result.Text
	}
	lastUser, lastActivity := -1, -1
	for i, part := range result.Parts {
		if part.Role == "user" {
			lastUser, lastActivity = i, i
		}
		if (part.Kind == "tool_use" || part.Kind == "tool_result" || part.Kind == "thinking") &&
			!(part.Native != nil && part.Native.Kind == "task" || part.Tool != nil && part.Tool.Kind == "task") {
			lastActivity = i
		}
	}
	final := []chatmodel.Part{}
	for i, part := range result.Parts {
		if i > lastUser && part.Role == "assistant" && part.Kind == "text" && part.Phase == "final_answer" && strings.TrimSpace(part.Text) != "" {
			final = append(final, part)
		}
	}
	if len(final) == 0 {
		for i, part := range result.Parts {
			if i > lastActivity && part.Role == "assistant" && part.Kind == "text" && part.Phase == "" && strings.TrimSpace(part.Text) != "" {
				final = append(final, part)
			}
		}
	}
	var text strings.Builder
	previousID := ""
	for _, part := range final {
		if text.Len() > 0 && (part.ID == "" || part.ID != previousID) {
			text.WriteString("\n\n")
		}
		text.WriteString(part.Text)
		previousID = part.ID
	}
	return text.String()
}

func (a *App) remoteConversationAllowed(peer, subject, user, origin, chat string) bool {
	var found bool
	err := a.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_agent_tasks WHERE peer_id=? AND subject=? AND origin_chat=? AND chat_id=?)`, peer, subject, origin, chat).Scan(&found)
	return err == nil && found && a.store.CanAccessChat(user, chat)
}

func (a *App) federationAgentTask(peer, subject, user string, request federationAgentPayload) (remoteAgentTask, error) {
	var task remoteAgentTask
	var body string
	err := a.store.db.QueryRow(`SELECT body FROM federation_agent_tasks WHERE id=? AND peer_id=? AND subject=? AND origin_chat=?`, request.TaskID, peer, subject, request.OriginChat).Scan(&body)
	if err != nil || json.Unmarshal([]byte(body), &task) != nil || !a.store.CanAccessChat(user, task.Conversation) {
		return remoteAgentTask{}, errors.New("Remote task access denied")
	}
	if task.Status != "running" {
		return task, nil
	}
	chat, ok := a.store.Get(task.Conversation)
	if !ok {
		return task, errors.New("Remote conversation unavailable")
	}
	if request.Operation == "cancel" {
		if !a.store.CanControlChat(user, chat.ID) {
			return task, errors.New("Remote task is read-only")
		}
		a.turnMu.Lock()
		control := a.activeTurns[chat.ID]
		if control != nil && control.id == task.TurnID {
			control.cancel()
		}
		a.turnMu.Unlock()
	}
	a.liveMu.Lock()
	turn, running := a.live[chat.ID]
	a.liveMu.Unlock()
	if running && turn.ID == task.TurnID {
		task.Text = ""
		task.Progress, task.UpdatedAt, task.Approval, task.TurnID = chatmodel.Clip(turn.Reply, 64<<10), time.Now().UnixMilli(), turn.PendingApproval, turn.ID
		task.Truncated = len(turn.Reply) > 64<<10
		if task.Approval != nil {
			task.Status = "awaiting_approval"
		}
	} else if running || !a.turnRunning(chat.ID) {
		var latestBody, state string
		if err := a.store.db.QueryRow(`SELECT state,body FROM federation_agent_tasks WHERE id=?`, task.ID).Scan(&state, &latestBody); err != nil {
			return task, err
		}
		if state != "running" {
			if err := json.Unmarshal([]byte(latestBody), &task); err != nil {
				return task, err
			}
			return task, nil
		}
		// Native transcript is authoritative. A crash can interrupt a task
		// before its completion receipt is saved; never report it as running forever.
		task.Status, task.Error = "interrupted", "Remote task interrupted; inspect history before retrying"
		body, _ := json.Marshal(task)
		_, _ = a.store.db.Exec(`UPDATE federation_agent_tasks SET state=?,body=? WHERE id=? AND state='running'`, task.Status, string(body), task.ID)
	}
	return task, nil
}

func (a *App) federationAgentConversations(peer, subject, user string, request federationAgentPayload) (any, error) {
	limit := request.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := a.store.db.Query(`SELECT t.chat_id,MAX(t.created_at) AS stamp FROM federation_agent_tasks t JOIN sessions s ON s.id=t.chat_id WHERE t.peer_id=? AND t.subject=? AND t.origin_chat=? AND (?='' OR s.project_id=?) AND (?='' OR t.chat_id<?) GROUP BY t.chat_id ORDER BY t.chat_id DESC LIMIT ?`, peer, subject, request.OriginChat, request.Project, request.Project, request.Before, request.Before, limit+1)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		var stamp int64
		if err := rows.Scan(&id, &stamp); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	items := []map[string]any{}
	for _, id := range ids {
		if !a.store.CanAccessChat(user, id) {
			continue
		}
		chat, ok := a.store.getSession(id)
		if ok {
			items = append(items, map[string]any{"conversation_id": chat.ID, "project_id": chat.ProjectID, "agent": chat.Backend, "title": chat.Title, "status": chat.Status, "updated_at": chat.UpdatedAt, "can_control": a.store.CanControlChat(user, id)})
		}
	}
	next := ""
	if more {
		next = ids[len(ids)-1]
	}
	return map[string]any{"conversations": items, "next": next}, nil
}

func (a *App) federationAgentHistory(ctx context.Context, peer, subject, user string, request federationAgentPayload) (any, error) {
	if !a.remoteConversationAllowed(peer, subject, user, request.OriginChat, request.Conversation) {
		return nil, errors.New("Remote conversation access denied")
	}
	chat, ok := a.store.Get(request.Conversation)
	if !ok {
		return nil, errors.New("Remote conversation unavailable")
	}
	limit := request.Limit
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	var err error
	native := strings.HasPrefix(request.Before, "native-") || request.Before == "" && chat.History != nil && chat.History.Native
	if native {
		// Page native history directly from the first request. Slicing the
		// newest cached window would produce intermediate after-* cursors
		// whose anchors can leave that window as the conversation grows.
		chat, err = a.store.codexHistoryPageWithLimit(ctx, chat, request.Before, limit)
	} else {
		chat, err = historyPage(a.snapshot(chat), request.Before)
	}
	if err != nil {
		return nil, err
	}
	if chat.History != nil && chat.History.Error != "" {
		return nil, errors.New("Remote native history is unavailable")
	}
	start := max(0, len(chat.Turns)-limit)
	if native {
		// The native reader applied the limit already, and may exceed it to keep
		// one response whole. Its cursor continues after everything it returned;
		// an after-* cursor from here would be read against the wrong window.
		start = 0
	}
	next := ""
	if chat.History != nil && chat.History.HasMore {
		next = chat.History.Before
	}
	if start > 0 {
		next = "after-" + historyCursor(chat.ID, chat.Turns[start-1])
	}
	items := []map[string]any{}
	for _, turn := range chat.Turns[start:] {
		messages := []map[string]string{}
		budget, truncated := 8192, false
		for _, part := range chat.Transcript.Parts[turn.PartStart:turn.PartEnd] {
			if part.Kind != "text" || part.Role != "user" && part.Role != "assistant" {
				continue
			}
			if budget <= 0 {
				truncated = truncated || part.Text != ""
				continue
			}
			text := chatmodel.Clip(part.Text, budget)
			if len(part.Text) > budget {
				truncated = true
			}
			budget -= len(text)
			if text != "" {
				messages = append(messages, map[string]string{"role": part.Role, "text": text})
			}
		}
		items = append(items, map[string]any{"id": turn.ID, "status": turn.Status, "model": turn.Model, "reasoning_effort": turn.ReasoningEffort, "messages": messages, "truncated": truncated})
	}
	return map[string]any{"conversation_id": chat.ID, "turns": items, "next": next}, nil
}

func (a *App) remoteAgent(ctx context.Context, identity authIdentity, originChat, originTurn string, operation remoteAgentOperation) (json.RawMessage, error) {
	if identity.User.SourceServerID != "" {
		return nil, errors.New("Transitive remote agent delegation is not permitted")
	}
	// Recheck the original source login rather than trusting a captured User.
	var active bool
	if err := a.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.token_hash=? AND a.user_id=? AND a.expires_at>? AND u.disabled=0)`, identity.TokenHash, identity.User.ID, time.Now().UnixMilli()).Scan(&active); err != nil || !active {
		return nil, errors.New("Source login is no longer active")
	}
	if originChat != "" && !a.store.CanControlChat(identity.User.ID, originChat) {
		return nil, errors.New("Parent conversation access denied")
	}
	if originChat == "" && operation.Operation != "servers" && operation.Operation != "projects" && operation.Operation != "agents" && operation.Operation != "permissions" && operation.Operation != "calls" {
		return nil, errors.New("Select a parent conversation before calling a remote agent")
	}
	if operation.Operation == "servers" {
		targets, err := a.store.federationTargets(identity)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(targets)
		return body, err
	}
	if operation.Operation == "calls" {
		return a.remoteAgentCalls(identity.User.ID, originChat, originTurn == "")
	}
	peer, err := federationPeer(a.store.db, operation.Server)
	if err != nil || !peer.allows(identity.User.ID) {
		return nil, errFederationDenied
	}
	if peer.Transport == "" {
		return nil, errors.New("Configure a server control route before delegating agents")
	}
	request := operation.federationAgentPayload
	request.OriginChat, request.OriginTurn, request.Result, request.Error, request.Wait = originChat, originTurn, nil, "", 0
	if request.Operation == "delegate" && request.RequestID == "" {
		request.RequestID = chatmodel.NewID()
	}
	delegated := request
	deadline := time.Now().Add(time.Duration(min(max(operation.WaitSeconds, 0), 25)) * time.Second)
	var known json.RawMessage
	for retried := false; ; {
		if request.Operation == "task" {
			// The target holds the query open. Rounding up never polls in a spin.
			request.Wait = min(federationAgentWaitSeconds, int((max(time.Until(deadline), 0)+time.Second-1)/time.Second))
		}
		ticket, err := a.store.federationIssue(identity, peer.ID)
		if err != nil {
			return nil, err
		}
		answer, err := a.federationSend(ctx, peer, federationMessage{Action: federationAgentAction, Value: ticket, Agent: &request})
		if err != nil {
			if known != nil {
				// The task is recorded. A failed refresh must not lose its reference.
				return known, nil
			}
			if request.Operation != "delegate" || errors.Is(err, errFederationDenied) {
				return nil, err
			}
			// The target may have started the task without its answer arriving.
			// The same request_id returns that task instead of running it again.
			if !retried && ctx.Err() == nil {
				retried = true
				continue
			}
			return nil, fmt.Errorf("Remote server did not confirm the delegation. Retry with request_id %s to recover it without running the task twice", request.RequestID)
		}
		if answer.Agent.Error != "" {
			return nil, errors.New(answer.Agent.Error)
		}
		if !json.Valid(answer.Agent.Result) {
			return nil, errors.New("Invalid remote agent response")
		}
		var task remoteAgentTask
		if request.Operation == "delegate" || request.Operation == "task" || request.Operation == "cancel" {
			if err := json.Unmarshal(answer.Agent.Result, &task); err != nil || !chatmodel.ValidID(task.ID) || !chatmodel.ValidID(task.Conversation) {
				return nil, errors.New("Invalid remote task response")
			}
			if request.Operation == "delegate" {
				if err := a.saveRemoteAgentCall(identity.User.ID, peer.ID, originChat, delegated.RequestID, delegated.Message, task); err != nil {
					return nil, err
				}
			} else {
				a.updateRemoteAgentCall(identity.User.ID, peer.ID, task)
			}
		}
		if task.Status != "running" || !time.Now().Before(deadline) || ctx.Err() != nil {
			return answer.Agent.Result, nil
		}
		// Only the task reference is needed from here on. The message and
		// context were delivered once; they are not signed and sent again.
		known = answer.Agent.Result
		request = federationAgentPayload{Operation: "task", OriginChat: originChat, OriginTurn: originTurn, TaskID: task.ID}
	}
}

func (a *App) saveRemoteAgentCall(user, peer, origin, request, message string, task remoteAgentTask) error {
	body, _ := json.Marshal(remoteAgentCall{Server: peer, OriginChat: origin, RequestID: request, Message: chatmodel.Clip(message, 8192), Task: task})
	_, err := a.store.db.Exec(`INSERT INTO federation_agent_calls VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,peer_id,request_id) DO UPDATE SET body=excluded.body,updated_at=excluded.updated_at`, user, origin, peer, request, string(body), time.Now().UnixMilli())
	return err
}

func (a *App) updateRemoteAgentCall(user, peer string, task remoteAgentTask) {
	rows, err := a.store.db.Query(`SELECT request_id,origin_chat,COALESCE(json_extract(body,'$.message'),'') FROM federation_agent_calls WHERE user_id=? AND peer_id=? AND json_extract(body,'$.task.task_id')=?`, user, peer, task.ID)
	if err != nil {
		return
	}
	type call struct{ request, origin, message string }
	var calls []call
	for rows.Next() {
		var c call
		if rows.Scan(&c.request, &c.origin, &c.message) == nil {
			calls = append(calls, c)
		}
	}
	rows.Close()
	for _, c := range calls {
		_ = a.saveRemoteAgentCall(user, peer, c.origin, c.request, c.message, task)
	}
}

func (a *App) remoteAgentCalls(user, origin string, includeHistory bool) (json.RawMessage, error) {
	rows, err := a.store.db.Query(`SELECT body FROM federation_agent_calls WHERE user_id=? AND origin_chat=? ORDER BY updated_at DESC LIMIT 20`, user, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var call remoteAgentCall
		if err := json.Unmarshal([]byte(body), &call); err != nil {
			return nil, err
		}
		if len(call.Task.Text) > 8192 {
			call.Task.Text = chatmodel.Clip(call.Task.Text, 8192)
			call.Task.Truncated = true
		}
		if len(call.Task.Progress) > 8192 {
			call.Task.Progress = chatmodel.Clip(call.Task.Progress, 8192)
			call.Task.Truncated = true
		}
		encoded, err := json.Marshal(call)
		if err != nil {
			return nil, err
		}
		items = append(items, encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// A locally discovered session can have been run by another Spool data
	// directory. Its transcript is viewable here, but its capabilities are not.
	// Recover display snapshots only; never turn them into live MCP references.
	if includeHistory {
		if chat, ok := a.store.getSession(origin); ok && chat.LocalSession {
			history, _ := a.store.remoteAgentHistory(chat)
			known := map[string]bool{}
			for _, body := range items {
				var call remoteAgentCall
				if json.Unmarshal(body, &call) == nil {
					known[call.Server+":"+call.Task.ID] = true
				}
			}
			for _, call := range history {
				if len(items) >= 20 {
					break
				}
				if !known[call.Server+":"+call.Task.ID] {
					body, err := json.Marshal(call)
					if err != nil {
						return nil, err
					}
					items = append(items, body)
				}
			}
		}
	}
	return json.Marshal(items)
}

func (a *App) federationAgentResponse(w http.ResponseWriter, r *http.Request, identity authIdentity) {
	var request remoteAgentOperation
	// A delegation carries a message and separate context, both JSON-escaped.
	if !decodeJSONLimit(w, r, &request, remoteAgentMaxBody) {
		return
	}
	result, err := a.remoteAgent(r.Context(), identity, request.OriginChat, "", request)
	if err != nil {
		apiError(w, http.StatusBadRequest, fmt.Sprint(err))
		return
	}
	apiJSON(w, http.StatusOK, result)
}
