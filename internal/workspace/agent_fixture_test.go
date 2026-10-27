package workspace

import (
	"context"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

func newAgentFixture(t *testing.T, backend chatmodel.Backend) agent.AgentRequest {
	t.Helper()
	fixture := agentfixture.New(t)
	return agent.AgentRequest{ChatID: fixture.ID, Backend: backend, Binary: fixture.Binary, WorkingDirectory: fixture.Directory, Prompt: "Hello"}
}
func fixtureLog(t *testing.T, request agent.AgentRequest) []map[string]any {
	t.Helper()
	return agentfixture.Log(t, request.WorkingDirectory)
}
func fixtureCount(t *testing.T, request agent.AgentRequest, method string) int {
	t.Helper()
	return agentfixture.Count(t, request.WorkingDirectory, method)
}
func fixtureCatalog() chatmodel.CodexCatalog { return agentfixture.Catalog() }
func TestAgentHelperProcess(t *testing.T)    { agentfixture.Run(t) }
func newTestRunner(t *testing.T) *agent.CLIRunner {
	t.Helper()
	runner := agent.NewCLIRunner()
	t.Cleanup(func() { _ = runner.Close() })
	return runner
}

func runFixture(t *testing.T, runner *agent.CLIRunner, request agent.AgentRequest) (agent.AgentResult, []agent.AgentEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var events []agent.AgentEvent
	result, err := runner.Run(ctx, request, func(event agent.AgentEvent) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatalf("fixture failed: %v (result=%#v)", err, result)
	}
	return result, events
}
