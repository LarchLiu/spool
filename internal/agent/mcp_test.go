package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestApplicationMCPNativeConfigurationAndCleanup(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			originals := map[string]string{
				filepath.Join(home, ".codex", "config.toml"):                              "model = \"user-model\"\n[mcp_servers.personal]\ncommand = \"user-mcp\"\n",
				filepath.Join(home, ".claude", "settings.json"):                           `{"permissions":{"allow":["Read"]}}`,
				filepath.Join(home, ".claude.json"):                                       `{"mcpServers":{"personal":{"command":"user-mcp"}}}`,
				filepath.Join(request.WorkingDirectory, ".mcp.json"):                      `{"mcpServers":{"project":{"command":"project-mcp"}}}`,
				filepath.Join(request.WorkingDirectory, ".claude", "settings.local.json"): `{"enabledMcpjsonServers":["project"]}`,
			}
			for path, text := range originals {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0640); err != nil {
					t.Fatal(err)
				}
			}
			request.MCP = &MCPServer{URL: "http://127.0.0.1:12345/mcp", Token: strings.Repeat("s", 43)}
			runner := newTestRunner(t)
			result, _ := runFixture(t, runner, request)
			var configPath string
			var found bool
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] == "thread/start" {
					found = true
					value := entry["value"].(map[string]any)
					params := value["params"].(map[string]any)
					config := params["config"].(map[string]any)
					if config["mcp_servers.spool_federation.url"] != request.MCP.URL || config["mcp_servers.spool_federation.env_http_headers"].(map[string]any)["Authorization"] != "SPOOL_AGENT_MCP_AUTHORIZATION" {
						t.Fatal("Codex lost MCP configuration", config)
					}
				}
				if entry["method"] == "spawn" && backend == chatmodel.BackendClaude {
					args := entry["args"].([]any)
					for i, arg := range args {
						if strings.Contains(arg.(string), request.MCP.Token) {
							t.Fatal("MCP capability leaked into command arguments")
						}
						if arg == "--mcp-config" {
							configPath = args[i+1].(string)
						}
					}
					body, err := os.ReadFile(configPath)
					if err != nil {
						t.Fatal(err)
					}
					var config map[string]any
					if json.Unmarshal(body, &config) != nil || !strings.Contains(string(body), request.MCP.Token) {
						t.Fatal("Claude lost private MCP configuration")
					}
					info, _ := os.Stat(configPath)
					if info.Mode().Perm() != 0600 {
						t.Fatal("MCP configuration is not private")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("MCP not injected")
			}
			// A different turn capability cannot reuse the previous process's auth.
			request.NativeSessionID = result.SessionID
			request.MCP = &MCPServer{URL: request.MCP.URL, Token: strings.Repeat("t", 43)}
			result, _ = runFixture(t, runner, request)
			if fixtureCount(t, request, "spawn") != 2 {
				t.Fatal("changed MCP capability reused a stale process")
			}
			if configPath != "" {
				if _, err := os.Stat(configPath); !os.IsNotExist(err) {
					t.Fatal("previous turn's MCP configuration survived its replacement", err)
				}
			}
			// Turning off injection resumes the native session in a process with
			// no Spool MCP arguments or app-server configuration overrides.
			request.NativeSessionID, request.MCP = result.SessionID, nil
			runFixture(t, runner, request)
			if fixtureCount(t, request, "spawn") != 3 {
				t.Fatal("disabled MCP retained the old process")
			}
			var lastSpawn, lastThread map[string]any
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] == "spawn" {
					lastSpawn = entry
				}
				if entry["method"] == "thread/resume" {
					lastThread = entry
				}
			}
			if backend == chatmodel.BackendClaude {
				for _, arg := range lastSpawn["args"].([]any) {
					if arg == "--mcp-config" || arg == "--strict-mcp-config" {
						t.Fatal("disabled injection changed Claude MCP configuration")
					}
				}
			} else {
				params := lastThread["value"].(map[string]any)["params"].(map[string]any)
				if params["config"] != nil {
					t.Fatal("disabled injection retained Codex overrides", params["config"])
				}
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			if configPath != "" {
				if _, err := os.Stat(configPath); !os.IsNotExist(err) {
					t.Fatal("private MCP configuration was not removed", err)
				}
			}
			for path, original := range originals {
				body, err := os.ReadFile(path)
				if err != nil || string(body) != original {
					t.Fatal("MCP injection modified existing agent configuration", path, err)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatal("MCP injection changed configuration permissions", path, err)
				}
			}
		})
	}
}
