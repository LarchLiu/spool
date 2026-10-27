package agent

import (
	"context"
	"testing"
	"time"

	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

func newAgentFixture(t *testing.T, backend chatmodel.Backend) AgentRequest {
	t.Helper()
	fixture := agentfixture.New(t)
	return AgentRequest{ChatID: fixture.ID, Backend: backend, Binary: fixture.Binary, WorkingDirectory: fixture.Directory, Prompt: "Hello"}
}
func fixtureLog(t *testing.T, request AgentRequest) []map[string]any {
	t.Helper()
	return agentfixture.Log(t, request.WorkingDirectory)
}
func fixtureCount(t *testing.T, request AgentRequest, method string) int {
	t.Helper()
	return agentfixture.Count(t, request.WorkingDirectory, method)
}
func fixtureCatalog() chatmodel.CodexCatalog { return agentfixture.Catalog() }
func TestAgentHelperProcess(t *testing.T)    { agentfixture.Run(t) }
func newTestRunner(t *testing.T) *CLIRunner {
	t.Helper()
	runner := NewCLIRunner()
	t.Cleanup(func() { _ = runner.Close() })
	return runner
}

func nextFixtureTurn(request AgentRequest, session string) AgentRequest {
	request.NativeSessionID = session
	return request
}

func runFixture(t *testing.T, runner *CLIRunner, request AgentRequest) (AgentResult, []AgentEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var events []AgentEvent
	result, err := runner.Run(ctx, request, func(event AgentEvent) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatalf("fixture failed: %v (result=%#v)", err, result)
	}
	return result, events
}
