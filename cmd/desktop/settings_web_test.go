package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
		if _, err := bridge.Execute(context.Background(), settingsCommand{Kind: "stop-service"}); err == nil {
			t.Fatal("mutation allowed without calling page")
		}
		if _, err := bridge.ChoosePath(context.Background(), "directory", ""); err == nil {
			t.Fatal("dialog allowed without calling page")
		}
		if _, err := bridge.ReleaseNotes(context.Background()); err == nil {
			t.Fatal("release history allowed without calling page")
		}
	}
}

func TestManagementDTOsDoNotChangeChatAPIProjection(t *testing.T) {
	project := workspace.Project{ID: "project", Name: "Demo", Path: "/private/project", Members: []string{"person"}, Issues: workspace.IssuesAutomation{Enabled: true}}
	agent := workspace.AgentInfo{Executable: "/private/agent", ResolvedPath: "/private/resolved"}
	public, err := json.Marshal([]any{project, agent})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"/private/", `"members"`, `"issues"`, `"executable"`, `"resolved_path"`} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("chat projection exposes %s: %s", secret, public)
		}
	}
	private, err := json.Marshal([]any{
		settingsProject{Project: project, Path: project.Path, Members: project.Members, Issues: project.Issues},
		settingsAgent{AgentInfo: agent, Executable: agent.Executable, ResolvedPath: agent.ResolvedPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"path":"/private/project"`, `"members":["person"]`, `"issues":`, `"executable":"/private/agent"`, `"resolved_path":"/private/resolved"`} {
		if !strings.Contains(string(private), key) {
			t.Fatalf("management projection missing %s: %s", key, private)
		}
	}
}

func TestSettingsCommandDecodesPrivateProjectFields(t *testing.T) {
	var command settingsCommand
	err := json.Unmarshal([]byte(`{"kind":"save-project","project":{"id":"one","name":"Project","path":"/tmp/project","members":["person"],"shared_chats":true,"issues":{"enabled":true,"interval_minutes":60,"backend":"codex","user_id":"person","assignee_ids":["person"],"labels":["bug"]}}}`), &command)
	if err != nil {
		t.Fatal(err)
	}
	if command.Project.ID != "one" || command.Project.Path != "/tmp/project" || !command.Project.SharedChats || len(command.Project.Members) != 1 || !command.Project.Issues.Enabled {
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
	for _, section := range []string{"Service", "People", "Projects", "Agents", "Online", "Appearance", "About"} {
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
