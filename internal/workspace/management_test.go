package workspace

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	settings.HistoryCacheMiB = 128
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
	if saved.Port != 18787 || saved.HistoryCacheMiB != 128 || other.historyCache.maxBytes != 128*1024*1024 || agents[0].Enabled || agents[0].Executable != "/custom/codex" || len(projects) != 1 || projects[0].ID != p.ID {
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

func TestHistoryCacheSettingsDefaultAndValidation(t *testing.T) {
	s := testStore(t)
	settings, err := s.Settings()
	if err != nil || settings.HistoryCacheMiB != 64 || s.historyCache.maxBytes != 64*1024*1024 {
		t.Fatal("default cache size was not applied", err)
	}
	// Existing settings JSON predates the cache field and must retain the default.
	if _, err := s.db.Exec(`INSERT INTO settings VALUES('server','{"host":"127.0.0.1","port":8787,"idle_minutes":15,"auto_start":true}')`); err != nil {
		t.Fatal(err)
	}
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	legacy, err := other.Settings()
	if err != nil || legacy.HistoryCacheMiB != 64 || other.historyCache.maxBytes != 64*1024*1024 {
		t.Fatal("old settings did not inherit the default cache size", err)
	}
	for _, size := range []int{0, -1, int(^uint(0)>>1)/(1024*1024) + 1} {
		invalid := settings
		invalid.HistoryCacheMiB = size
		if err := s.SaveSettings(invalid); err == nil {
			t.Fatalf("invalid cache size %d accepted", size)
		}
		persisted, err := s.Settings()
		if err != nil || persisted.HistoryCacheMiB != 64 || s.historyCache.maxBytes != 64*1024*1024 {
			t.Fatal("invalid cache size changed settings or the live cache", err)
		}
	}
}

func TestHistoryCacheSettingsResizeImmediatelyEvictsWholeSessions(t *testing.T) {
	s := testStore(t)
	settings := DefaultSettings()
	settings.HistoryCacheMiB = 2
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	_, reads := countNativeHistoryReads(s)
	root := t.TempDir()
	paths := []string{filepath.Join(root, "first.jsonl"), filepath.Join(root, "second.jsonl"), filepath.Join(root, "third.jsonl")}
	load := func(path string) {
		t.Helper()
		snapshot, err := s.readHistory(chatmodel.Session{ID: path, Backend: chatmodel.BackendCodex, NativeSessionPath: path})
		if err != nil || len(snapshot.Turns) != 1 || len(snapshot.Transcript.Parts) != 52 {
			t.Fatal("cache resize lost full session content", err)
		}
	}
	for _, path := range paths {
		appendNativeTurns(t, path, 0, 1, 50)
		load(path)
	}
	if len(s.historyCache.files) != 3 || reads.Load() != 3 {
		t.Fatal("initial cache size not applied")
	}
	load(paths[1]) // Make the second session most recently used.
	settings.HistoryCacheMiB = 1
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if len(s.historyCache.files) != 2 || s.historyCache.bytes > 1024*1024 || s.historyCache.maxBytes != 1024*1024 {
		t.Fatal("saving a smaller target did not immediately trim the cache")
	}
	load(paths[1])
	load(paths[2])
	if reads.Load() != 3 {
		t.Fatal("resize evicted a recent session")
	}
	load(paths[0])
	if reads.Load() != 4 {
		t.Fatal("resize did not evict the least recently used session")
	}
	settings.HistoryCacheMiB = 4
	if err := s.SaveSettings(settings); err != nil || s.historyCache.maxBytes != 4*1024*1024 {
		t.Fatal("larger target was not applied immediately", err)
	}
	oversized := filepath.Join(root, "oversized.jsonl")
	appendNativeTurns(t, oversized, 0, 1, 160)
	bigChat := chatmodel.Session{ID: "oversized", Backend: chatmodel.BackendCodex, NativeSessionPath: oversized}
	if _, err := s.readHistory(bigChat); err != nil {
		t.Fatal(err)
	}
	settings.HistoryCacheMiB = 1
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.readHistory(bigChat)
	if err != nil || len(snapshot.Transcript.Parts) != 162 || len(s.historyCache.files) != 1 || s.historyCache.bytes < 1280000 || reads.Load() != 5 {
		t.Fatal("resize evicted or truncated the single oversized session", err)
	}
}

func TestHistoryCacheSettingsConcurrentSavesAndReads(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	paths := []string{filepath.Join(root, "first.jsonl"), filepath.Join(root, "second.jsonl")}
	for _, path := range paths {
		appendNativeTurns(t, path, 0, 2, 40)
	}
	var wait sync.WaitGroup
	for reader := range 4 {
		wait.Go(func() {
			for range 4 {
				snapshot, err := s.readHistory(chatmodel.Session{ID: "concurrent", Backend: chatmodel.BackendCodex, NativeSessionPath: paths[reader%2]})
				if err != nil || len(snapshot.Turns) != 2 || len(snapshot.Transcript.Parts) != 84 {
					t.Errorf("concurrent resize lost history: %v", err)
				}
			}
		})
	}
	for writer := range 2 {
		wait.Go(func() {
			settings := DefaultSettings()
			settings.HistoryCacheMiB = writer + 1
			for range 4 {
				if err := s.SaveSettings(settings); err != nil {
					t.Errorf("concurrent settings save: %v", err)
				}
			}
		})
	}
	wait.Wait()
	saved, err := s.Settings()
	if err != nil || s.historyCache.maxBytes != saved.HistoryCacheMiB*1024*1024 {
		t.Fatal("live cache target differs from persisted settings", err)
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
	v, err := s.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := s.Get(v.ID)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_turn BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`); err != nil {
		t.Fatal(err)
	}
	// A fresh chat can switch agents, so this must reach the failing SQL write.
	if _, err := s.beginTurn(v.ID, "No write", chatmodel.BackendClaude, chatmodel.CodexOptions{Model: "test-claude", ReasoningEffort: "high"}); err == nil || !strings.Contains(err.Error(), "fixture write failure") {
		t.Fatalf("expected injected database failure, got %v", err)
	}
	if saved, ok := s.Get(v.ID); !ok || !reflect.DeepEqual(saved, original) {
		t.Fatalf("partial failed turn or agent switch persisted: %#v", saved)
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
