package agent

import (
	"context"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexDiscoversCatalogWithoutStartingChatAndCachesCopy(t *testing.T) {
	fixture := newAgentFixture(t, chatmodel.BackendCodex)
	t.Setenv("SPOOL_CODEX_BIN", fixture.Binary)
	runner := newTestRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	catalog, err := runner.CodexModels(ctx)
	if err != nil || len(catalog.Models) != 3 || catalog.DefaultModel != "test-model" || catalog.DefaultEffort != "high" {
		t.Fatalf("unexpected catalog: %#v, error=%v", catalog, err)
	}
	if fixtureCount(t, fixture, "thread/start") != 0 || fixtureCount(t, fixture, "turn/start") != 0 || fixtureCount(t, fixture, "model/list") != 2 {
		t.Fatal("discovery started a chat or did not load all catalog pages")
	}
	catalog.Models[0].DisplayName = "Mutated"
	catalog.Models[0].Efforts[0].Effort = "mutated"
	cached, err := runner.CodexModels(ctx)
	if err != nil || cached.Models[0].DisplayName != "Test Model" || cached.Models[0].Efforts[0].Effort != "low" || fixtureCount(t, fixture, "spawn") != 1 {
		t.Fatalf("catalog cache is not independent: %#v, error=%v", cached, err)
	}
	if len(runner.chats) != 0 {
		t.Fatal("catalog query kept a chat process alive")
	}
}

func TestCodexModelAndLevelSwitchReuseProcessAndSurviveResume(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	runner := newTestRunner(t)
	runner.IdleTimeout = 300 * time.Millisecond
	choices := []chatmodel.CodexOptions{{Model: "test-model", ReasoningEffort: "xhigh"}, {Model: "other-model", ReasoningEffort: "low"}}
	for _, choice := range choices {
		request.Model, request.ReasoningEffort = choice.Model, choice.ReasoningEffort
		result, events := runFixture(t, runner, request)
		if result.Model != choice.Model || result.ReasoningEffort != choice.ReasoningEffort {
			t.Fatalf("result has stale selection: %#v", result)
		}
		found := false
		for _, event := range events {
			if event.Type == "model" && event.Model == choice.Model && event.ReasoningEffort == choice.ReasoningEffort {
				found = true
			}
		}
		if !found {
			t.Fatal("current model/level was not streamed")
		}
		request = nextFixtureTurn(request, result.SessionID)
	}
	if fixtureCount(t, request, "spawn") != 1 {
		t.Fatal("changing model restarted the CLI process")
	}
	waitProcessExit(t, currentProcess(t, runner, request.ChatID))
	request.Model, request.ReasoningEffort = "test-model", "high"
	runFixture(t, runner, request)
	var turns []map[string]any
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		params, _ := value["params"].(map[string]any)
		switch entry["method"] {
		case "turn/start":
			turns = append(turns, params)
		case "thread/resume":
			if params["model"] != "test-model" || params["threadId"] != request.NativeSessionID {
				t.Fatalf("resume lost settings: %#v", params)
			}
		}
	}
	choices = append(choices, chatmodel.CodexOptions{Model: "test-model", ReasoningEffort: "high"})
	if len(turns) != len(choices) {
		t.Fatalf("unexpected turn count: %d", len(turns))
	}
	for i, choice := range choices {
		if turns[i]["model"] != choice.Model || turns[i]["effort"] != choice.ReasoningEffort {
			t.Errorf("turn %d did not send model/effort: %#v", i, turns[i])
		}
	}
}
