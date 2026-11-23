package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexResumeRecoversOnlyEmptyProviderBeforeSendingMessage(t *testing.T) {
	for _, tc := range []struct {
		name, savedProvider, config, configError, wantError, wantProvider string
		resumes, configReads, turns                                       int
	}{
		{name: "empty provider with custom default", wantProvider: "fixture-provider", resumes: 2, configReads: 1, turns: 1},
		{name: "empty provider with built-in default", config: `{"model_provider":null}`, wantProvider: "openai", resumes: 2, configReads: 1, turns: 1},
		{name: "removed named provider", savedProvider: "retired-provider", wantError: "provider “retired-provider”", resumes: 1},
		{name: "configuration unavailable", configError: "configuration unavailable", wantError: "configuration unavailable", resumes: 1, configReads: 1},
		{name: "no current provider", config: `{"model_provider":""}`, wantError: "provider “”", resumes: 1, configReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendCodex)
			request.NativeSessionID = "converted-native-session"
			request.NativeSessionPath = filepath.Join(request.WorkingDirectory, "converted.jsonl")
			request.Model = "selected-model"
			request.ReasoningEffort = "high"
			request.PermissionMode = "auto"
			files := map[string]string{"fixture-resume-provider": tc.savedProvider}
			if tc.config != "" {
				files["fixture-config.json"] = tc.config
			}
			if tc.configError != "" {
				files["fixture-config-error"] = tc.configError
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(request.WorkingDirectory, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := newTestRunner(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, request, nil)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
			} else if err != nil || result.SessionID != request.NativeSessionID || result.Model != request.Model || result.Text == "" {
				t.Fatalf("converted session did not continue: %+v, %v", result, err)
			}
			if got := fixtureCount(t, request, "thread/resume"); got != tc.resumes {
				t.Fatalf("resume attempts = %d, want %d", got, tc.resumes)
			}
			if got := fixtureCount(t, request, "config/read"); got != tc.configReads {
				t.Fatalf("configuration reads = %d, want %d", got, tc.configReads)
			}
			if got := fixtureCount(t, request, "turn/start"); got != tc.turns {
				t.Fatalf("model requests = %d, want %d", got, tc.turns)
			}
			var initial map[string]any
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] != "thread/resume" {
					continue
				}
				value, _ := entry["value"].(map[string]any)
				params, _ := value["params"].(map[string]any)
				if initial == nil {
					initial = params
					continue
				}
				if params["modelProvider"] != tc.wantProvider {
					t.Fatalf("retry did not use the configured provider: %+v", params)
				}
				delete(params, "modelProvider")
				if !reflect.DeepEqual(initial, params) {
					t.Fatalf("provider recovery changed resume options: %+v != %+v", initial, params)
				}
			}
		})
	}
}
