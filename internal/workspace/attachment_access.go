package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// UserAttachmentPath is used only by the local desktop's user-initiated native
// file chooser. It grants no HTTP endpoint and never changes project access.
func (s *Service) UserAttachmentPath(token, chatID, path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") || protectedProjectPath(path) {
		return "", os.ErrPermission
	}
	identity, err := s.Store.authenticate(token)
	if err != nil || !s.Store.CanAccessChat(identity.User.ID, chatID) {
		return "", os.ErrPermission
	}
	chat, ok := s.Store.Get(chatID)
	if !ok || !sessionUserAttachment(chat, path) {
		return "", os.ErrPermission
	}
	resolved := canonicalDirectory(path)
	if resolved == "" || protectedProjectPath(resolved) || directoryWithin(resolved, s.Store.Root()) || chat.NativeSessionPath != "" && resolved == canonicalDirectory(chat.NativeSessionPath) {
		return "", os.ErrPermission
	}
	for _, variable := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if home := canonicalDirectory(os.Getenv(variable)); home != "" && directoryWithin(resolved, home) {
			return "", os.ErrPermission
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", os.ErrPermission
	}
	return path, nil
}
