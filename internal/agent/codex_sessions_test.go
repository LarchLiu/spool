package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexSessionListUsesNativePaginationWithoutStartingChat(t *testing.T) {
	fixture := newAgentFixture(t, chatmodel.BackendCodex)
	path := filepath.Join(fixture.WorkingDirectory, "native.jsonl")
	pages := map[string]any{
		"": map[string]any{"data": []any{
			map[string]any{"id": "newest-thread", "cwd": fixture.WorkingDirectory, "path": path, "name": "Native name", "preview": "First prompt", "createdAt": 10, "updatedAt": 30, "source": "appServer"},
			map[string]any{"id": "child-thread", "cwd": fixture.WorkingDirectory, "source": map[string]any{"subAgent": "review"}},
			map[string]any{"id": "temporary", "ephemeral": true},
		}, "nextCursor": "older-thread"},
		"older-thread": map[string]any{"data": []any{
			map[string]any{"id": "newest-thread", "updatedAt": 30},
			map[string]any{"id": "older-thread", "cwd": fixture.WorkingDirectory, "path": path, "preview": "Preview title", "createdAt": 5, "updatedAt": 20},
		}, "nextCursor": nil},
	}
	body, _ := json.Marshal(pages)
	if err := os.WriteFile(filepath.Join(fixture.WorkingDirectory, "fixture-thread-pages.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	source := CodexSessionLister{Binary: fixture.Binary}
	sessions, err := source.ListSessions(context.Background(), ListSessionsOptions{})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("native list: %+v %v", sessions, err)
	}
	if sessions[0].Title != "Native name" || sessions[0].UpdatedAt != 30000 || sessions[1].Title != "Preview title" || sessions[1].CreatedAt != 5000 || !sessions[0].TitleAuthoritative || sessions[1].TitleAuthoritative {
		t.Fatalf("metadata not mapped: %+v", sessions)
	}
	for _, entry := range fixtureLog(t, fixture) {
		if entry["method"] != "thread/list" {
			continue
		}
		value, _ := entry["value"].(map[string]any)
		params, _ := value["params"].(map[string]any)
		if params["sortKey"] != "updated_at" || params["archived"] != false || !reflect.DeepEqual(params["modelProviders"], []any{}) || !reflect.DeepEqual(params["sourceKinds"], []any{"cli", "vscode", "exec", "appServer", "unknown"}) {
			t.Fatalf("incorrect native scope: %+v", params)
		}
	}
	if fixtureCount(t, fixture, "thread/list") != 2 || fixtureCount(t, fixture, "thread/start") != 0 || fixtureCount(t, fixture, "turn/start") != 0 {
		t.Fatal("listing started a chat or missed a page")
	}
	page, err := source.ListSessions(context.Background(), ListSessionsOptions{Offset: 1, Limit: 1, Directory: fixture.WorkingDirectory})
	if err != nil || len(page) != 1 || page[0].ID != "older-thread" {
		t.Fatalf("filtered pagination: %+v %v", page, err)
	}
	entries := fixtureLog(t, fixture)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["method"] != "thread/list" {
			continue
		}
		value, _ := entries[i]["value"].(map[string]any)
		params, _ := value["params"].(map[string]any)
		if params["cwd"] != fixture.WorkingDirectory {
			t.Fatalf("exact cwd filter was not sent: %+v", params)
		}
		break
	}
	pages["older-thread"] = pages[""]
	body, _ = json.Marshal(pages)
	if err := os.WriteFile(filepath.Join(fixture.WorkingDirectory, "fixture-thread-pages.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ListSessions(context.Background(), ListSessionsOptions{}); err == nil {
		t.Fatal("repeated pagination cursor was accepted")
	}
}

func TestCodexSubagentSourceUsesStructure(t *testing.T) {
	for source, want := range map[string]bool{
		`"cli"`: false, `"appServer"`: false, `null`: false, ``: false,
		`{"custom":"my-subagent-runner"}`: false, `"my-subagent-runner"`: false,
		`{"subAgent":"review"}`: true, `{"subagent":{"thread_spawn":{}}}`: true, `"subAgentReview"`: true, `"sub_agent"`: true,
	} {
		if got := codexSubagentSource(json.RawMessage(source)); got != want {
			t.Errorf("source %s: subagent=%v, want %v", source, got, want)
		}
	}
}

func TestCodexSessionListNativeSmoke(t *testing.T) {
	binary := os.Getenv("SPOOL_CODEX_LIST_SMOKE")
	if binary == "" {
		t.Skip("optional installed app-server smoke is not configured")
	}
	home, directory := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	converted, err := writeCodexCarry(home, directory, "openai", []carryItem{{Kind: "user", Text: "Isolated list smoke"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sessions, err := (CodexSessionLister{Binary: binary}).ListSessions(ctx, ListSessionsOptions{})
	if err != nil || len(sessions) != 1 || sessions[0].ID != converted.ID || sessions[0].Path != converted.Path || sessions[0].Directory != directory {
		t.Fatalf("native thread/list smoke: %+v %v", sessions, err)
	}
}
