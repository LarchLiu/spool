package workspace

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	chatmodel "spool/internal/model"
)

type messageInput struct {
	Message        string            `json:"message"`
	Model          string            `json:"model"`
	Effort         string            `json:"reasoning_effort"`
	Backend        chatmodel.Backend `json:"backend"`
	PermissionMode string            `json:"permission_mode"`
	files          []uploadedAttachment
}

func readMessageInput(w http.ResponseWriter, r *http.Request) (messageInput, bool) {
	var input messageInput
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
		ok := decodeJSON(w, r, &input)
		return input, ok
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+chatmodel.MaxMessageBytes+65536)
	reader, err := r.MultipartReader()
	if err != nil {
		apiError(w, 400, "Invalid attachment upload")
		return input, false
	}
	fields := map[string]*string{"message": &input.Message, "model": &input.Model, "reasoning_effort": &input.Effort, "permission_mode": &input.PermissionMode}
	var backend string
	fields["backend"] = &backend
	seen := map[string]bool{}
	total := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			apiError(w, 400, "Invalid attachment upload")
			return input, false
		}
		if part.FormName() == "files" && part.FileName() != "" {
			if len(input.files) >= maxAttachments {
				apiError(w, 400, "Attach at most 6 files, up to 16 MB total")
				return input, false
			}
			file, err := readAttachment(part)
			if err != nil {
				apiError(w, 400, err.Error())
				return input, false
			}
			total += file.Size
			if total > maxUploadBytes {
				apiError(w, 400, "Attach at most 6 files, up to 16 MB total")
				return input, false
			}
			input.files = append(input.files, file)
		} else {
			field, ok := fields[part.FormName()]
			if !ok || seen[part.FormName()] {
				apiError(w, 400, "Invalid attachment upload")
				return input, false
			}
			seen[part.FormName()] = true
			data, err := io.ReadAll(io.LimitReader(part, chatmodel.MaxMessageBytes+1))
			if err != nil || len(data) > chatmodel.MaxMessageBytes {
				apiError(w, 400, "Invalid attachment upload")
				return input, false
			}
			*field = string(data)
		}
		if err := part.Close(); err != nil {
			apiError(w, 400, "Invalid attachment upload")
			return input, false
		}
	}
	input.Backend = chatmodel.Backend(backend)
	return input, true
}

func (a *App) permissionCatalog(ctx context.Context, chat chatmodel.Session) (chatmodel.PermissionCatalog, error) {
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		return chatmodel.PermissionCatalog{}, err
	}
	provider, ok := a.runner.(interface {
		PermissionModes(context.Context, chatmodel.Backend, string) (chatmodel.PermissionCatalog, error)
	})
	if !ok {
		return chatmodel.PermissionCatalog{}, fmt.Errorf("Permission discovery is unavailable")
	}
	catalog, err := provider.PermissionModes(ctx, chat.Backend, project.Path)
	if err != nil {
		return chatmodel.PermissionCatalog{}, err
	}
	if chat.PermissionMode != "" {
		catalog.Current = chat.PermissionMode
	}
	return catalog, nil
}

func (a *App) permissionResponse(w http.ResponseWriter, r *http.Request, user string) {
	chat := chatmodel.Session{ProjectID: r.URL.Query().Get("project_id"), Backend: chatmodel.Backend(r.URL.Query().Get("backend"))}
	if !a.store.CanAccessProject(user, chat.ProjectID) || !chat.Backend.Valid() {
		apiError(w, 404, "Permission discovery is unavailable")
		return
	}
	if id := r.URL.Query().Get("chat_id"); id != "" {
		owned, ok := a.accessibleChat(user, id)
		if !ok || owned.ProjectID != chat.ProjectID {
			apiError(w, 404, "聊天不存在")
			return
		}
		if owned.Backend == chat.Backend {
			chat.PermissionMode = owned.PermissionMode
		}
	}
	if !a.enabled(chat.Backend) {
		apiError(w, 409, "agent 未启用或尚未安装")
		return
	}
	catalog, err := a.permissionCatalog(r.Context(), chat)
	if err != nil {
		apiError(w, 503, "Permission discovery is unavailable")
		return
	}
	apiJSON(w, 200, catalog)
}

type pendingApproval struct {
	chatID   string
	turnID   string
	decision chan bool
	decided  bool
}

func (a *App) requestApproval(ctx context.Context, chatID, turnID string, request chatmodel.ApprovalRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	request.ID = chatmodel.NewID()
	pending := &pendingApproval{chatID: chatID, turnID: turnID, decision: make(chan bool, 1)}
	a.liveMu.Lock()
	current, exists := a.live[chatID]
	if !exists || current.ID != turnID || current.Status != chatmodel.StatusRunning || ctx.Err() != nil {
		a.liveMu.Unlock()
		return false, context.Canceled
	}
	a.approvals[request.ID] = pending
	turn := a.live[chatID]
	turn.PendingApproval = &request
	a.live[chatID] = turn
	a.liveMu.Unlock()
	defer func() {
		a.liveMu.Lock()
		delete(a.approvals, request.ID)
		turn := a.live[chatID]
		if turn.PendingApproval != nil && turn.PendingApproval.ID == request.ID {
			turn.PendingApproval = nil
			a.live[chatID] = turn
		}
		a.liveMu.Unlock()
	}()
	select {
	case allowed := <-pending.decision:
		return allowed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-a.shutdown:
		return false, context.Canceled
	}
}

func (a *App) approvalResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session) {
	var input struct {
		ID     string `json:"id"`
		TurnID string `json:"turn_id"`
		Allow  bool   `json:"allow"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	a.liveMu.Lock()
	pending, ok := a.approvals[input.ID]
	if !ok || pending.chatID != chat.ID || pending.turnID != input.TurnID || pending.decided || chat.Status != chatmodel.StatusRunning {
		a.liveMu.Unlock()
		apiError(w, 409, "This approval request is no longer pending")
		return
	}
	pending.decided = true
	pending.decision <- input.Allow
	a.liveMu.Unlock()
	apiJSON(w, 200, map[string]bool{"ok": true})
}
