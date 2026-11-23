package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexModelProviderReadsProjectConfigWithoutStartingThread(t *testing.T) {
	for _, tc := range []struct {
		name, config, configError, provider, wantError string
	}{
		{name: "custom provider", provider: "fixture-provider"},
		{name: "built-in default", config: `{"model_provider":null}`, provider: "openai"},
		{name: "explicit empty", config: `{"model_provider":""}`, wantError: "empty model provider"},
		{name: "whitespace", config: `{"model_provider":" \t"}`, wantError: "empty model provider"},
		{name: "missing config", config: `null`, wantError: "no configuration"},
		{name: "config error", configError: "configuration unavailable", wantError: "configuration unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendCodex)
			for name, body := range map[string]string{"fixture-config.json": tc.config, "fixture-config-error": tc.configError} {
				if body != "" {
					if err := os.WriteFile(filepath.Join(request.WorkingDirectory, name), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{chatmodel.BackendCodex: request.Binary})
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			directory := t.TempDir()
			provider, err := runner.CodexModelProvider(ctx, directory)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
			} else if err != nil || provider != tc.provider {
				t.Fatalf("effective provider = %q, %v, want %q", provider, err, tc.provider)
			}
			if got := fixtureCount(t, request, "config/read"); got != 1 {
				t.Fatalf("config reads = %d, want 1", got)
			}
			for _, method := range []string{"thread/start", "thread/resume", "turn/start"} {
				if got := fixtureCount(t, request, method); got != 0 {
					t.Fatalf("provider lookup sent %s", method)
				}
			}
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] == "config/read" {
					value, _ := entry["value"].(map[string]any)
					params, _ := value["params"].(map[string]any)
					if params["cwd"] != directory {
						t.Fatalf("config lookup ignored project cwd: %+v", params)
					}
				}
			}
		})
	}
}
