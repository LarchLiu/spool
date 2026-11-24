package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	chatmodel "spool/internal/model"
)

func TestDescriptionSummaryNativeParameters(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.SummaryOnly, request.SystemPrompt = true, "Maintain concise descriptions."
			request.Model, request.ReasoningEffort = "test-model", "high"
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "fixture-config.json"), []byte(`{"mcp_servers":{"local":{"command":"fixture"},"remote":{"url":"https://example.test"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			result, _ := runFixture(t, newTestRunner(t), request)
			if result.Model != request.Model || result.ReasoningEffort != request.ReasoningEffort {
				t.Fatalf("summary lost selected model/effort: %#v", result)
			}
			var checked bool
			for _, entry := range fixtureLog(t, request) {
				value, _ := entry["value"].(map[string]any)
				params, _ := value["params"].(map[string]any)
				switch entry["method"] {
				case "thread/start":
					checked = true
					if params["baseInstructions"] != request.SystemPrompt || params["sandbox"] != "read-only" || params["approvalPolicy"] != "never" || params["ephemeral"] != true {
						t.Fatalf("summary thread not configured: %#v", params)
					}
					config, _ := params["config"].(map[string]any)
					if config["web_search"] != "disabled" || config["features.multi_agent"] != false || config["mcp_servers.local.enabled"] != false || config["mcp_servers.remote.enabled"] != false {
						t.Fatalf("summary inherited external tools: %#v", config)
					}
				case "turn/start":
					policy, _ := params["sandboxPolicy"].(map[string]any)
					if params["approvalPolicy"] != "never" || policy["type"] != "readOnly" || params["model"] != request.Model || params["effort"] != request.ReasoningEffort {
						t.Fatalf("summary turn lost configuration: %#v", params)
					}
				case "spawn":
					if backend != chatmodel.BackendClaude {
						continue
					}
					checked = true
					var args []string
					for _, arg := range entry["args"].([]any) {
						args = append(args, arg.(string))
					}
					for _, flag := range []string{"--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"} {
						if !slices.Contains(args, flag) {
							t.Fatalf("missing %s: %#v", flag, args)
						}
					}
					for flag, expected := range map[string]string{"--tools": "", "--mcp-config": `{"mcpServers":{}}`, "--system-prompt": request.SystemPrompt} {
						i := slices.Index(args, flag)
						if i < 0 || i+1 >= len(args) || args[i+1] != expected {
							t.Fatalf("wrong %s: %#v", flag, args)
						}
					}
				}
			}
			if !checked {
				t.Fatal("summary did not start a native session")
			}
		})
	}
}

func TestDescriptionProviderModelDescriptions(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			var catalog chatmodel.CodexCatalog
			var err error
			if backend == chatmodel.BackendCodex {
				runner := newTestRunner(t)
				runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: request.Binary})
				catalog, err = runner.CodexModels(context.Background())
			} else {
				catalog, err = DiscoverClaudeModels(context.Background(), request.Binary)
			}
			if err != nil || len(catalog.Models) == 0 || catalog.Models[0].Description == "" {
				t.Fatalf("provider description was lost: %#v, %v", catalog, err)
			}
		})
	}
}
