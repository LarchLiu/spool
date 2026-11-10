package agent

import (
	"errors"
	"testing"

	chatmodel "spool/internal/model"
	"spool/internal/netproxy"
)

func TestProxyChangesReplaceIdleAgentsAndPreserveSession(t *testing.T) {
	restore, err := netproxy.Prepare(t.Context(), netproxy.Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netproxy.Activate(restore) })
	t.Setenv("SPOOL_TEST_PROXY_ENV", "1")
	t.Setenv("HTTPS_PROXY", "http://inherited.invalid:1")
	t.Setenv("ALL_PROXY", "socks5://inherited.invalid:2")
	t.Setenv("NO_PROXY", "*")
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			policy, err := netproxy.Prepare(t.Context(), netproxy.Options{Enabled: true, ProxyURL: "http://proxy.invalid:8889", NoProxy: "internal.example"})
			if err != nil {
				t.Fatal(err)
			}
			netproxy.Activate(policy)
			request := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			result, _ := runFixture(t, runner, request)
			first := currentProcess(t, runner, request.ChatID)
			policy, err = netproxy.Prepare(t.Context(), netproxy.Options{})
			if err != nil {
				t.Fatal(err)
			}
			netproxy.Activate(policy)
			select {
			case <-first.done:
				t.Fatal("proxy save interrupted an existing agent")
			default:
			}
			request = nextFixtureTurn(request, result.SessionID)
			result, _ = runFixture(t, runner, request)
			second := currentProcess(t, runner, request.ChatID)
			if first == second || result.SessionID != request.NativeSessionID {
				t.Fatal("next turn reused a stale proxy or lost the native session")
			}
			runFixture(t, runner, request)
			if currentProcess(t, runner, request.ChatID) != second {
				t.Fatal("unchanged proxy unnecessarily replaced the agent")
			}
			var snapshots []map[string]any
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] == "proxy-env" {
					snapshots = append(snapshots, entry)
				}
			}
			if len(snapshots) != 2 || snapshots[0]["https"] != "http://proxy.invalid:8889" || snapshots[1]["http"] != "" || snapshots[1]["https"] != "" || snapshots[1]["all"] != "" || snapshots[1]["no"] != "" {
				t.Fatal("agent subprocess did not receive custom/direct proxy", snapshots)
			}
			unavailable := errors.New("saved proxy unavailable")
			netproxy.Reject(unavailable)
			if _, err := runner.Run(t.Context(), request, nil); !errors.Is(err, unavailable) {
				t.Fatal("idle agent bypassed unavailable policy", err)
			}
		})
	}
}
