package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"spool"
	"spool/internal/workspace"
)

func TestDesktopVersionPrefersPackageAndFallsBackToBuildMetadata(t *testing.T) {
	previous := mygo.App.Version()
	t.Cleanup(func() { mygo.App.SetVersion(previous) })
	mygo.App.SetVersion("v9.8.7")
	if desktopVersion() != "9.8.7" {
		t.Fatal("packaged version was not used")
	}
	mygo.App.SetVersion("")
	if version := desktopVersion(); version != spool.Version() || version == "" || version == "development" {
		t.Fatalf("unpackaged desktop has no current version: %q", version)
	}
}

func TestSettingsOnlyAllowsExactPrivateDocument(t *testing.T) {
	for _, address := range []string{settingsPageURL, "http://mygo.localhost/desktop.html"} {
		if !settingsDocumentAllowed(address) {
			t.Errorf("private document denied: %s", address)
		}
	}
	for _, address := range []string{
		"", "about:blank", "file:///desktop.html", "http://localhost:8787/desktop.html", "http://127.0.0.1:8787/desktop.html",
		"https://mygo.localhost/desktop.html", "mygo://localhost/", "mygo://localhost/desktop.html/", "mygo://localhost:80/desktop.html",
		"mygo://user@localhost/desktop.html", "mygo://evil.example/desktop.html", "mygo://localhost/%64esktop.html",
		settingsPageURL + "?", settingsPageURL + "?admin=true", settingsPageURL + "#", settingsPageURL + "#settings",
		"javascript:alert(1)", "data:text/html,hello",
	} {
		if settingsDocumentAllowed(address) {
			t.Errorf("non-private document accepted: %s", address)
		}
	}
}

func TestSettingsBridgeRejectsCallsWithoutDesktopPage(t *testing.T) {
	for _, bridge := range []*desktopSettings{{}, {panel: &panel{}}} {
		if _, err := bridge.Snapshot(context.Background()); err == nil {
			t.Fatal("snapshot allowed without calling page")
		}
		for _, kind := range []string{"stop-service", "save-tsnet-auth-key", "tsnet-login", "copy-tsnet-url"} {
			if _, err := bridge.Execute(context.Background(), settingsCommand{Kind: kind}); err == nil {
				t.Fatal("mutation allowed without calling page", kind)
			}
		}
		if _, err := bridge.ChoosePath(context.Background(), "directory", ""); err == nil {
			t.Fatal("dialog allowed without calling page")
		}
		if _, err := bridge.ReleaseNotes(context.Background()); err == nil {
			t.Fatal("release history allowed without calling page")
		}
		if _, err := bridge.TSNetLogin(context.Background()); err == nil {
			t.Fatal("login request allowed without calling page")
		}
		if _, err := bridge.TSNetRegister(context.Background(), "tskey-auth-test"); err == nil {
			t.Fatal("key registration allowed without calling page")
		}
		if _, err := bridge.Action(context.Background(), settingsCommand{Kind: "copy-tsnet-url"}); err == nil {
			t.Fatal("clipboard action allowed without calling page")
		}
	}
}

func TestSettingsActionIsSeparateFromMutationReloads(t *testing.T) {
	for _, kind := range []string{"navigate", "open-chat", "open-browser", "show-file", "copy-tsnet-url"} {
		if !settingsActionAllowed(kind) {
			t.Fatal("missing action", kind)
		}
	}
	for _, kind := range []string{"save-settings", "save-person", "restart-service", "restart-update", "unknown"} {
		if settingsActionAllowed(kind) {
			t.Fatal("mutation classified as UI action", kind)
		}
	}
	data, err := os.ReadFile("settings_web.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, "func (s *desktopSettings) Action(")
	end := strings.Index(source, "func (s *desktopSettings) Execute(")
	if start < 0 || end < start {
		t.Fatal("missing independent actions method")
	}
	action := source[start:end]
	for _, forbidden := range []string{"s.mu.", "p.load()", "applyPresentation()", "s.Snapshot("} {
		if strings.Contains(action, forbidden) {
			t.Fatal("lightweight action causes full refresh or locks settings", forbidden)
		}
	}
	if !strings.Contains(action, "settingsUI(") {
		t.Fatal("missing caller-page authorization")
	}
}

func TestManagementDTOsDoNotChangeChatAPIProjection(t *testing.T) {
	project := workspace.Project{ID: "project", Name: "Demo", Path: "/private/project", Members: []string{"person"}, MemberAccess: map[string]string{"person": "read-only"}, MemberOwnAccess: map[string]string{"person": "read-only"}, Issues: workspace.IssuesAutomation{Enabled: true}}
	agent := workspace.AgentInfo{Executable: "/private/agent", ResolvedPath: "/private/resolved"}
	public, err := json.Marshal([]any{project, agent})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"/private/", `"members"`, `"member_access"`, `"member_own_access"`, `"issues"`, `"executable"`, `"resolved_path"`} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("chat projection exposes %s: %s", secret, public)
		}
	}
	private, err := json.Marshal([]any{
		settingsProject{Project: project, Path: project.Path, Members: project.Members, MemberAccess: project.MemberAccess, MemberOwnAccess: project.MemberOwnAccess, Issues: project.Issues},
		settingsAgent{AgentInfo: agent, Executable: agent.Executable, ResolvedPath: agent.ResolvedPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"path":"/private/project"`, `"members":["person"]`, `"member_access":{"person":"read-only"}`, `"member_own_access":{"person":"read-only"}`, `"issues":`, `"executable":"/private/agent"`, `"resolved_path":"/private/resolved"`} {
		if !strings.Contains(string(private), key) {
			t.Fatalf("management projection missing %s: %s", key, private)
		}
	}
}

func TestSettingsCommandDecodesPrivateProjectFields(t *testing.T) {
	var command settingsCommand
	err := json.Unmarshal([]byte(`{"kind":"save-project","project":{"id":"one","name":"Project","path":"/tmp/project","members":["person"],"member_access":{"person":"read-only"},"member_own_access":{"person":"read-only"},"shared_chats":true,"issues":{"enabled":true,"interval_minutes":60,"backend":"codex","user_id":"person","assignee_ids":["person"],"labels":["bug"]}}}`), &command)
	if err != nil {
		t.Fatal(err)
	}
	if command.Project.ID != "one" || command.Project.Path != "/tmp/project" || !command.Project.SharedChats || command.Project.MemberAccess["person"] != "read-only" || command.Project.MemberOwnAccess["person"] != "read-only" || len(command.Project.Members) != 1 || !command.Project.Issues.Enabled {
		t.Fatalf("lost management fields: %+v", command)
	}
}

func TestSettingsAssetsForbidRemoteContentAndHTTPAdminFallback(t *testing.T) {
	handler, err := settingsAssets()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", settingsPageURL, nil))
	policy := response.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"script-src 'self'", "connect-src 'none'", "frame-src 'none'", "object-src 'none'", "form-action 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Errorf("CSP lacks %s: %s", directive, policy)
		}
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
}

func TestSettingsSectionsAreAllowlisted(t *testing.T) {
	for _, section := range []string{"Service", "People", "Projects", "Agents", "Online", "Network", "Appearance", "About"} {
		if !settingsTabAllowed(section) {
			t.Fatal(section)
		}
	}
	for _, section := range []string{"Chat", "", "service", "javascript:alert(1)"} {
		if settingsTabAllowed(section) {
			t.Fatal(section)
		}
	}
}

func TestSettingsLoginUsesAnIndependentWorkerWithoutMutationLock(t *testing.T) {
	source, err := os.ReadFile("settings_web.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "func (s *desktopSettings) TSNetLogin(")
	end := strings.Index(text, "func (s *desktopSettings) Execute(")
	if start < 0 || end < start {
		t.Fatal("missing independent login method")
	}
	login := text[start:end]
	if strings.Contains(login, "s.mu.") || strings.Contains(login, "s.Snapshot(") {
		t.Fatal("login locks settings mutations or returns a stale full snapshot")
	}
	if !strings.Contains(login, "settingsUI(") || !strings.Contains(login, "s.loginMu.TryLock()") {
		t.Fatal("login lost its page authorization or duplicate-request guard")
	}
}
