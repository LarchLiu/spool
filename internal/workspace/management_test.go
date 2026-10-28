package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

func TestSettingsUsersProjectsAndAgentsPersistInSQLite(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	settings := DefaultSettings()
	settings.Port = 18787
	settings.Host = "0.0.0.0"
	settings.PublicURL = "https://chat.example.test"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureAgent(chatmodel.BackendCodex, false, "/custom/codex"); err != nil {
		t.Fatal(err)
	}
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	saved, _ := other.Settings()
	agents, _ := other.Agents()
	projects, _ := other.UserProjects(u.ID)
	if saved.Port != 18787 || agents[0].Enabled || agents[0].Executable != "/custom/codex" || len(projects) != 1 || projects[0].ID != p.ID {
		t.Fatal("management state did not persist")
	}
	if _, err = s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex); err != nil {
		t.Fatal(err)
	}
	p.Path = t.TempDir()
	if _, err = s.SaveProject(p); err == nil {
		t.Fatal("project directory changed under existing chats")
	}
	bad := settings
	bad.Port = 70000
	if s.SaveSettings(bad) == nil {
		t.Fatal("invalid port persisted")
	}
	p.Path = projects[0].Path
	p.SharedChats = true
	p.Members = nil
	if _, err = s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if s.CanAccessProject(u.ID, p.ID) {
		t.Fatal("revoked membership retained")
	}
}

func TestSharedProjectSessionsAreVisibleToMembers(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	bob := testUser(t, s, "bob")
	project := testProject(t, s, alice, bob)
	chat, err := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if sessions, err := s.UserSessions(bob.ID, project.ID); err != nil || len(sessions) != 0 {
		t.Fatal("private project exposed chats")
	}
	project.SharedChats = true
	if _, err := s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	sessions, err := s.UserSessions(bob.ID, project.ID)
	if err != nil || len(sessions) != 1 || sessions[0].ID != chat.ID {
		t.Fatalf("shared project sessions missing: %#v, %v", sessions, err)
	}
	navigation, err := s.UserNavigation(bob.ID)
	if err != nil || len(navigation.Chats) != 1 || navigation.Chats[0].ID != chat.ID {
		t.Fatalf("shared project navigation missing chat: %#v, %v", navigation.Chats, err)
	}
	if !s.CanAccessChat(bob.ID, chat.ID) {
		t.Fatal("shared member cannot access chat")
	}
	outsider := testUser(t, s, "outsider")
	if s.CanAccessChat(outsider.ID, chat.ID) {
		t.Fatal("non-member accessed shared chat")
	}
}
func TestRegistrySavesDynamicModelsWithoutUserPromptAndRetainsOnFailure(t *testing.T) {
	s := testStore(t)
	codex := newAgentFixture(t, chatmodel.BackendCodex)
	claude := newAgentFixture(t, chatmodel.BackendClaude)
	s.ConfigureAgent(chatmodel.BackendCodex, true, codex.Binary)
	s.ConfigureAgent(chatmodel.BackendClaude, true, claude.Binary)
	r := NewRegistry(s)
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Agents()
	if !all[0].Installed || len(all[0].Catalog.Models) != 3 || all[0].Catalog.DefaultModel != "test-model" || all[0].Error != "" {
		t.Fatalf("Codex discovery: %#v", all[0])
	}
	if !all[1].Installed || len(all[1].Catalog.Models) != 1 || all[1].Catalog.Models[0].Model != "test-claude" || all[1].Error != "" {
		t.Fatalf("Claude discovery: %#v", all[1])
	}
	levels := all[1].Catalog.Models[0].Efforts
	if len(levels) != 2 || levels[0].Effort != "low" || levels[1].Effort != "high" {
		t.Fatalf("Claude effort discovery lost advertised levels: %#v", levels)
	}
	if fixtureCount(t, codex, "turn/start") != 0 || fixtureCount(t, claude, "user") != 0 {
		t.Fatal("discovery consumed a user turn")
	}
	s.ConfigureAgent(chatmodel.BackendCodex, true, filepath.Join(t.TempDir(), "missing"))
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, _ = s.Agents()
	if all[0].Installed || all[0].Error == "" || len(all[0].Catalog.Models) != 3 || all[0].LastSuccessAt == 0 {
		t.Fatal("failed discovery erased the cached catalog")
	}
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	persisted, _ := other.Agents()
	if len(persisted[0].Catalog.Models) != 3 {
		t.Fatal("models not persisted")
	}
}
func TestSQLiteTurnWriteRollsBackAtomically(t *testing.T) {
	s := testStore(t)
	v, _ := s.createSession("", "", chatmodel.BackendCodex)
	s.db.Exec(`CREATE TRIGGER reject_turn BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`)
	if _, err := s.BeginTurn(v.ID, "No write", chatmodel.CodexOptions{Model: "test-model", ReasoningEffort: "high"}); err == nil {
		t.Fatal("expected database failure")
	}
	saved, _ := s.Get(v.ID)
	if saved.Status != chatmodel.StatusReady || saved.Model != "" || len(saved.Turns) != 0 {
		t.Fatal("partial failed turn persisted")
	}
}
func TestServiceLeasePreventsConcurrentManagers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPOOL_CODEX_BIN", filepath.Join(root, "missing-codex"))
	t.Setenv("SPOOL_CLAUDE_BIN", filepath.Join(root, "missing-claude"))
	service, err := OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenService(root); err == nil {
		t.Fatal("second manager acquired same database")
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Close()
	service.Close()
}
func TestLongTaskDisconnectAndResumeUseProjectCWD(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	project := testProject(t, s, u)
	fixture := newAgentFixture(t, chatmodel.BackendCodex) // The fixture intentionally logs in its own directory, but inspects the RPC cwd.
	s.saveAgent(AgentInfo{Backend: chatmodel.BackendCodex, Enabled: true, Installed: true, Catalog: fixtureCatalog()})
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{chatmodel.BackendCodex: fixture.Binary})
	runner.IdleTimeout = 150 * time.Millisecond
	a := NewApp(s, runner, "", "")
	defer a.Close()
	chat, _ := s.CreateChat(u.ID, project.ID, chatmodel.BackendCodex)
	if err := a.startTurn(chat, "silent-task", chatmodel.CodexOptions{Model: "test-model", ReasoningEffort: "high"}); err != nil {
		t.Fatal(err)
	}
	done := waitChat(t, s, chat.ID)
	if done.Status != chatmodel.StatusCompleted || done.NativeSessionID == "" {
		t.Fatal("detached task not saved")
	}
	agentfixture.WaitExit(t, agentfixture.PID(t, fixture.WorkingDirectory))
	chat, _ = s.Get(chat.ID)
	if err := a.startTurn(chat, "Continue", chatmodel.CodexOptions{Model: "test-model", ReasoningEffort: "low"}); err != nil {
		t.Fatal(err)
	}
	waitChat(t, s, chat.ID)
	for _, entry := range fixtureLog(t, fixture) {
		if entry["method"] == "turn/start" {
			value, _ := entry["value"].(map[string]any)
			params, _ := value["params"].(map[string]any)
			if params["cwd"] != project.Path {
				t.Fatal("agent did not use project directory")
			}
		}
	}
	if fixtureCount(t, fixture, "thread/resume") != 1 {
		t.Fatal("idle session not resumed")
	}
}
