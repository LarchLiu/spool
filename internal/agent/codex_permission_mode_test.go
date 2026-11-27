package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestCodexFullAccessDiscoveryHonorsEachPolicyAndNeverBecomesImplicitDefault(t *testing.T) {
	for _, tc := range []struct {
		name, policy string
		available    bool
	}{
		{"unrestricted", `{}`, true},
		{"sandbox restricted", `{"allowedSandboxModes":["workspace-write"]}`, false},
		{"approval restricted", `{"allowedApprovalPolicies":["on-request"]}`, false},
		{"reviewer restricted", `{"allowedApprovalsReviewers":["auto_review"]}`, false},
		{"only full access", `{"allowedSandboxModes":["danger-full-access"],"allowedApprovalPolicies":["never"]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendCodex)
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "fixture-policy.json"), []byte(tc.policy), 0600); err != nil {
				t.Fatal(err)
			}
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{request.Backend: request.Binary})
			catalog, err := runner.PermissionModes(t.Context(), request.Backend, request.WorkingDirectory)
			if err != nil || (catalog.Validate("fullAccess") == nil) != tc.available {
				t.Fatalf("wrong discovery: %+v, %v", catalog, err)
			}
			if catalog.Default == "fullAccess" || catalog.Current == "fullAccess" {
				t.Fatal("discovery implicitly enabled full access")
			}
			if fixtureCount(t, request, "thread/start") != 0 || fixtureCount(t, request, "turn/start") != 0 {
				t.Fatal("discovery created a thread or submitted inference")
			}
		})
	}
}

func TestCodexFullAccessAppliesOnStartResumeAndReusedTurnsAndRestoresSandbox(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "resumed"}[resume], func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendCodex)
			if resume {
				request.NativeSessionID = "existing-native-session"
			}
			runner := newTestRunner(t)
			modes := []struct{ mode, policy, sandbox, reviewer string }{
				{"fullAccess", "never", "dangerFullAccess", "user"},
				{"ask", "on-request", "workspaceWrite", "user"},
				{"auto", "on-request", "workspaceWrite", "auto_review"},
				{"fullAccess", "never", "dangerFullAccess", "user"},
			}
			for _, mode := range modes {
				request.PermissionMode = mode.mode
				result, _ := runFixture(t, runner, request)
				request.NativeSessionID = result.SessionID
			}
			turn := 0
			for _, entry := range fixtureLog(t, request) {
				value, _ := entry["value"].(map[string]any)
				params, _ := value["params"].(map[string]any)
				if entry["method"] == "thread/start" || entry["method"] == "thread/resume" {
					if params["sandbox"] != "danger-full-access" || params["approvalPolicy"] != "never" {
						t.Fatalf("thread did not receive full access: %+v", params)
					}
				}
				if entry["method"] != "turn/start" {
					continue
				}
				want := modes[turn]
				policy, _ := params["sandboxPolicy"].(map[string]any)
				if params["approvalPolicy"] != want.policy || params["approvalsReviewer"] != want.reviewer || policy["type"] != want.sandbox {
					t.Fatalf("wrong permissions on turn %d: %+v", turn, params)
				}
				if want.mode == "fullAccess" && len(policy) != 1 || want.mode != "fullAccess" && policy["networkAccess"] != false {
					t.Fatalf("sandbox did not switch cleanly: %+v", policy)
				}
				turn++
			}
			if turn != len(modes) || fixtureCount(t, request, "spawn") != 1 {
				t.Fatal("permission changes lost a turn or restarted the session")
			}
			request.Prompt = "/review"
			runFixture(t, runner, request)
			if fixtureCount(t, request, "review/start") != 1 || fixtureCount(t, request, "thread/settings/update") != 1 {
				t.Fatal("review did not apply the selected permissions")
			}
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] == "thread/settings/update" {
					value, _ := entry["value"].(map[string]any)
					params, _ := value["params"].(map[string]any)
					if params["approvalPolicy"] != "never" || nestedString(params, "sandboxPolicy", "type") != "dangerFullAccess" {
						t.Fatalf("review lost full access: %+v", params)
					}
				}
			}
		})
	}
}

func TestCodexFullAccessRejectsUnacknowledgedOrChangedPermissionsBeforeInference(t *testing.T) {
	for _, applied := range []string{
		`{"approvalPolicy":"on-request"}`,
		`{"sandbox":{"type":"workspaceWrite"}}`,
		`{"approvalPolicy":null,"sandbox":null}`,
		`{"approvalsReviewer":"auto_review"}`,
	} {
		t.Run(applied, func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendCodex)
			request.PermissionMode = "fullAccess"
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "fixture-applied-permissions.json"), []byte(applied), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := newTestRunner(t).Run(context.Background(), request, nil)
			if err == nil || !strings.Contains(err.Error(), "did not apply the selected permission mode") || fixtureCount(t, request, "turn/start") != 0 {
				t.Fatalf("unapplied permissions submitted inference: %v", err)
			}
		})
	}
}
