package workspace

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Optional browser check; all app data, sessions, and browser profiles belong to
// the run. A supplied native session is read only to locate its original Word
// attachment; neither its messages nor document contents are retained.
func TestUserAttachmentBrowser(t *testing.T) {
	if os.Getenv("CHROME_BIN") == "" {
		t.Skip("set CHROME_BIN for the attachment browser smoke")
	}
	file := filepath.Join(t.TempDir(), "合同_标红稿.docx")
	if err := os.WriteFile(file, officeWordFixture(t), 0600); err != nil {
		t.Fatal(err)
	}
	if source := os.Getenv("SPOOL_ATTACHMENT_NATIVE"); source != "" {
		turns, err := agent.IndexSessionTurns(chatmodel.BackendCodex, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		file = ""
		for _, turn := range turns {
			transcript, err := agent.ReadSessionTurn(chatmodel.BackendCodex, source, turn)
			if err != nil {
				t.Fatal(err)
			}
			for _, part := range transcript.Parts {
				if part.Role == "user" && part.Kind == "context" && strings.HasSuffix(part.Name, ".docx") {
					file = part.FilePath
					break
				}
			}
			if file != "" {
				break
			}
		}
		if file == "" {
			t.Fatal("no Word attachment in the supplied native session")
		}
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	owner := testUser(t, s, "owner")
	p := testProject(t, s, owner)
	chat, err := s.CreateChat(owner.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(t.TempDir(), "native.jsonl")
	for _, message := range []struct{ role, text string }{
		{"user", fmt.Sprintf("# Files mentioned by the user:\n\n## %s: %s\n\n## My request:\nReview the attached Word file.", info.Name(), file)},
		{"assistant", "[Open attachment](<" + file + ">)"},
	} {
		appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": message.role, "content": []any{map[string]any{"type": "text", "text": message.text}}}})
	}
	if err := s.SetNativeSessionPath(chat.ID, "attachment-browser-native", native); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	server := httptest.NewServer(a)
	t.Cleanup(server.CloseClientConnections)
	t.Cleanup(server.Close)
	probe := exec.Command("node", "web/test/attachment-preview-smoke.mjs")
	probe.Dir = filepath.Join("..", "..")
	probe.Env = append(os.Environ(), "SPOOL_ATTACHMENT_ORIGIN="+server.URL, "SPOOL_ATTACHMENT_CHAT="+chat.ID, fmt.Sprintf("SPOOL_ATTACHMENT_BYTES=%d", info.Size()))
	output, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("attachment browser smoke: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
