package workspace

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func (a *App) renameChatResponse(w http.ResponseWriter, r *http.Request, user string, chat chatmodel.Session) {
	var input struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	title := strings.Join(strings.Fields(input.Title), " ")
	if title == "" || utf8.RuneCountInString(title) > 200 || strings.ContainsFunc(title, unicode.IsControl) {
		apiError(w, 400, "Enter a chat title of 1–200 characters")
		return
	}
	// Serialize renames without holding the store or turn admission locks
	// while waiting on a provider. Running conversations can still be renamed.
	a.renameMu.Lock()
	defer a.renameMu.Unlock()
	chat, ok := a.store.getSession(chat.ID)
	if !ok || !a.store.CanControlChat(user, chat.ID) {
		apiError(w, 404, "聊天不存在")
		return
	}
	if chat.NativeSessionID == "" {
		apiError(w, 409, "Send a message before renaming this chat")
		return
	}
	renamer, ok := a.runner.(agent.SessionRenamer)
	if !ok {
		apiError(w, 503, "Could not rename chat")
		return
	}
	directory, err := a.store.sessionDirectory(chat)
	if err != nil {
		apiError(w, 409, "project directory is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err = renamer.RenameSession(ctx, agent.AgentRequest{
		ChatID: chat.ID, Backend: chat.Backend, WorkingDirectory: directory,
		NativeSessionID: chat.NativeSessionID, NativeSessionPath: chat.NativeSessionPath,
	}, title)
	if err != nil {
		apiError(w, 502, "Could not rename chat")
		return
	}
	// Do not let an older paginated-history window undo the successful rename.
	if chat.Backend == chatmodel.BackendCodex {
		a.store.codexHistory.rename(chat.NativeSessionID, title)
	}
	a.store.mu.Lock()
	result, err := a.store.db.Exec(`UPDATE sessions SET title=? WHERE id=? AND backend=? AND native_session_id=?`, title, chat.ID, chat.Backend, chat.NativeSessionID)
	a.store.mu.Unlock()
	if err != nil {
		apiError(w, 500, "Could not rename chat")
		return
	}
	if count, _ := result.RowsAffected(); count != 1 {
		apiError(w, 409, "Chat session changed. Try again.")
		return
	}
	a.store.changes.publish(changeNavigation)
	chat, _ = a.store.getSession(chat.ID)
	apiJSON(w, 200, sessionMetadata(chat))
}
