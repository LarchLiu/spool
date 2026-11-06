package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	chatmodel "spool/internal/model"
)

func TestDesktopAttachmentAccessRequiresCurrentChatUserAndExplicitFile(t *testing.T) {
	s := testStore(t)
	owner, other := testUser(t, s, "owner"), testUser(t, s, "other")
	p := testProject(t, s, owner, other)
	chat, err := s.CreateChat(owner.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	file := writeSessionFile(t, root, "合同.docx", "attachment")
	neighbor := writeSessionFile(t, root, "neighbor.txt", "unattached")
	protected := writeSessionFile(t, root, ".env.local", "protected")
	stored := writeSessionFile(t, s.Root(), "internal.txt", "store file")
	provider := t.TempDir()
	t.Setenv("CODEX_HOME", provider)
	providerFile := writeSessionFile(t, provider, "provider.txt", "provider file")
	alias := filepath.Join(root, "alias.docx")
	if err := os.Symlink(file, alias); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(t.TempDir(), "native.jsonl")
	var prompt strings.Builder
	prompt.WriteString("# Files mentioned by the user:\n\n")
	for _, path := range []string{file, protected, stored, providerFile, alias, native, root} {
		fmt.Fprintf(&prompt, "## %s: %s\n\n", filepath.Base(path), path)
	}
	prompt.WriteString("## My request:\nReview these files.")
	appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt.String()}}}})
	if err := s.SetNativeSessionPath(chat.ID, "attachment-access-native", native); err != nil {
		t.Fatal(err)
	}
	app := NewApp(s, &fakeRunner{}, "", "")
	ownerCookie, _ := loginRequest(t, app, owner.Username)
	otherCookie, _ := loginRequest(t, app, other.Username)
	service := &Service{Store: s}
	if path, err := service.UserAttachmentPath(ownerCookie.Value, chat.ID, file); err != nil || path != file {
		t.Fatalf("attached file was rejected: %q %v", path, err)
	}
	for _, tc := range []struct{ name, token, chat, path string }{
		{"no session", "", chat.ID, file},
		{"invalid session", "invalid", chat.ID, file},
		{"another user", otherCookie.Value, chat.ID, file},
		{"another chat", ownerCookie.Value, "missing-chat", file},
		{"unattached neighbor", ownerCookie.Value, chat.ID, neighbor},
		{"protected file", ownerCookie.Value, chat.ID, protected},
		{"store file", ownerCookie.Value, chat.ID, stored},
		{"provider file", ownerCookie.Value, chat.ID, providerFile},
		{"native history", ownerCookie.Value, chat.ID, native},
		{"symlink", ownerCookie.Value, chat.ID, alias},
		{"directory", ownerCookie.Value, chat.ID, root},
		{"relative file", ownerCookie.Value, chat.ID, "合同.docx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.UserAttachmentPath(tc.token, tc.chat, tc.path); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("expected permission rejection, got %v", err)
			}
		})
	}
	if _, err := s.db.Exec("DELETE FROM auth_sessions WHERE token_hash=?", tokenHash(ownerCookie.Value)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UserAttachmentPath(ownerCookie.Value, chat.ID, file); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("revoked session retained access: %v", err)
	}
}

func TestSessionFileOSDenialIsDistinctFromProjectPolicy(t *testing.T) {
	for _, denied := range []error{os.ErrPermission, syscall.EPERM} {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Language", "zh-CN")
		sessionFileReadError(w, &os.PathError{Op: "open", Path: "attachment.docx", Err: denied})
		var response map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != 403 || response["code"] != "file_read_denied" || !strings.Contains(response["error"], "操作系统") {
			t.Fatalf("OS denial was not localized or identified: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	projectFileError(w, os.ErrPermission)
	if w.Code != 403 || strings.Contains(w.Body.String(), "file_read_denied") {
		t.Fatalf("project policy incorrectly offered OS access: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	sessionFileReadError(w, os.ErrNotExist)
	if w.Code != 404 || strings.Contains(w.Body.String(), "file_read_denied") {
		t.Fatalf("missing file incorrectly offered OS access: %d %s", w.Code, w.Body.String())
	}
}
