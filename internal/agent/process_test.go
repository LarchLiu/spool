package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func currentProcess(t *testing.T, runner *CLIRunner, chatID string) *agentProcess {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	chat := runner.chats[chatID]
	if chat == nil || chat.process == nil {
		t.Fatal("chat process not found")
	}
	return chat.process
}

func waitProcessExit(t *testing.T, process *agentProcess) {
	t.Helper()
	select {
	case <-process.done:
	case <-time.After(4 * time.Second):
		t.Fatal("CLI process was not reclaimed")
	}
}

func TestSetIdleTimeoutReschedulesExistingIdleChats(t *testing.T) {
	runner := newTestRunner(t)
	runner.SetIdleTimeout(3 * time.Minute)
	chat, err := runner.reserve(AgentRequest{ChatID: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	runner.release("idle", chat, true)
	// Account for time already spent idle, without waiting minutes in the test.
	idleSince := time.Now().Add(-2 * time.Minute)
	runner.mu.Lock()
	chat.idleSince = idleSince
	runner.scheduleIdleLocked("idle", chat)
	runner.mu.Unlock()
	for _, idle := range []time.Duration{10 * time.Minute, 0, -time.Second, time.Minute} {
		runner.SetIdleTimeout(idle)
		want := idle
		if want <= 0 {
			want = defaultAgentIdleTimeout
		}
		runner.mu.Lock()
		deadline := chat.deadline
		runner.mu.Unlock()
		if !deadline.Equal(idleSince.Add(want)) {
			t.Fatalf("timeout %v reset the idle start: deadline=%v", idle, deadline)
		}
		if want > 2*time.Minute && chat.ctx.Err() != nil {
			t.Fatal("extending the timeout stopped an idle chat")
		}
	}
	select {
	case <-chat.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shorter timeout did not reclaim an already expired idle chat")
	}
}

func TestSetIdleTimeoutDoesNotInterruptRunningTurns(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "block-task"
			runner := newTestRunner(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			started, finished := make(chan struct{}, 1), make(chan error, 1)
			go func() {
				_, err := runner.Run(ctx, request, func(event AgentEvent) error {
					if event.Type == "delta" {
						select {
						case started <- struct{}{}:
						default:
						}
					}
					return nil
				})
				finished <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("turn did not start")
			}
			runner.SetIdleTimeout(time.Millisecond)
			select {
			case err := <-finished:
				t.Fatal("changing the idle timeout stopped a running turn", err)
			case <-time.After(30 * time.Millisecond):
			}
			cancel()
			if err := <-finished; !errors.Is(err, context.Canceled) {
				t.Fatal("turn did not remain available for cancellation", err)
			}
		})
	}
}

func TestSetIdleTimeoutAppliesAfterRunningTurnCompletes(t *testing.T) {
	runner := newTestRunner(t)
	chat, err := runner.reserve(AgentRequest{ChatID: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	runner.SetIdleTimeout(time.Nanosecond)
	if chat.ctx.Err() != nil || chat.timer != nil {
		t.Fatal("a running turn was scheduled for idle reclamation")
	}
	runner.release("busy", chat, true)
	select {
	case <-chat.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("completed turn did not use the updated timeout")
	}
}

func TestRunningSilentTaskHasNoIdleOrTurnDeadline(t *testing.T) {
	if defaultAgentIdleTimeout != 15*time.Minute {
		t.Fatalf("default idle timeout = %s", defaultAgentIdleTimeout)
	}
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "silent-task"
			runner := newTestRunner(t)
			runner.IdleTimeout = 80 * time.Millisecond
			start := time.Now()
			runFixture(t, runner, request)
			if time.Since(start) < 300*time.Millisecond {
				t.Fatal("fixture did not exercise a long silent task")
			}
			process := currentProcess(t, runner, request.ChatID)
			select {
			case <-process.done:
				t.Fatal("process exited immediately after replying")
			default:
			}
			waitProcessExit(t, process)
		})
	}
}

func TestNewUserInputStopsIdleTimerUntilNextReply(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			runner.IdleTimeout = 200 * time.Millisecond
			result, _ := runFixture(t, runner, request)
			process := currentProcess(t, runner, request.ChatID)
			time.Sleep(50 * time.Millisecond)
			request = nextFixtureTurn(request, result.SessionID)
			request.Prompt = "silent-task"
			runFixture(t, runner, request)
			time.Sleep(80 * time.Millisecond)
			select {
			case <-process.done:
				t.Fatal("old timer killed the active/recently completed turn")
			default:
			}
			if fixtureCount(t, request, "spawn") != 1 {
				t.Fatal("new input did not reuse the same process")
			}
			waitProcessExit(t, process)
		})
	}
}

func TestIdleCLIOutputDoesNotExtendWaitingForUser(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "idle-chatter"
			runner := newTestRunner(t)
			runner.IdleTimeout = 100 * time.Millisecond
			runFixture(t, runner, request)
			waitProcessExit(t, currentProcess(t, runner, request.ChatID))
		})
	}
}

func TestExpiredProcessResumesSameProviderSession(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			runner.IdleTimeout = 80 * time.Millisecond
			first, _ := runFixture(t, runner, request)
			waitProcessExit(t, currentProcess(t, runner, request.ChatID))
			request = nextFixtureTurn(request, first.SessionID)
			second, events := runFixture(t, runner, request)
			if first.SessionID != second.SessionID || fixtureCount(t, request, "spawn") != 2 {
				t.Fatalf("did not resume expired process: first=%#v, second=%#v", first, second)
			}
			for _, event := range events {
				if event.Text == "Preparing isolated workspace" {
					t.Fatal("resumed chat announced workspace creation")
				}
			}
			if backend == chatmodel.BackendCodex && fixtureCount(t, request, "thread/resume") != 1 {
				t.Fatal("replacement Codex app-server did not resume the thread")
			}
			if backend == chatmodel.BackendClaude {
				found := false
				for _, entry := range fixtureLog(t, request) {
					if entry["method"] == "spawn" {
						args, _ := entry["args"].([]any)
						for i := 0; i+1 < len(args); i++ {
							if args[i] == "--resume" && args[i+1] == first.SessionID {
								found = true
							}
						}
					}
				}
				if !found {
					t.Fatal("replacement Claude process did not receive --resume")
				}
			}
		})
	}
}

func TestChatsUseSeparateCLIProcesses(t *testing.T) {
	runner := newTestRunner(t)
	codex := newAgentFixture(t, chatmodel.BackendCodex)
	claude := newAgentFixture(t, chatmodel.BackendClaude)
	runFixture(t, runner, codex)
	runFixture(t, runner, claude)
	first := currentProcess(t, runner, codex.ChatID)
	second := currentProcess(t, runner, claude.ChatID)
	if first.cmd.Process.Pid == second.cmd.Process.Pid {
		t.Fatal("different chats share one CLI process")
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	waitProcessExit(t, first)
	waitProcessExit(t, second)
}

func TestDifferentChatsCanRunAtTheSameTime(t *testing.T) {
	runner := newTestRunner(t)
	requests := []AgentRequest{newAgentFixture(t, chatmodel.BackendCodex), newAgentFixture(t, chatmodel.BackendClaude)}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	finished := make(chan error, 2)
	for _, request := range requests {
		request.Prompt = "block-task"
		go func(request AgentRequest) {
			_, err := runner.Run(ctx, request, func(event AgentEvent) error {
				if event.Type == "delta" {
					started <- struct{}{}
				}
				return nil
			})
			finished <- err
		}(request)
	}
	for range requests {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("one chat blocked another chat from starting")
		}
	}
	cancel()
	for range requests {
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected stop error: %v", err)
		}
	}
}

func TestChatsInSameDirectoryCanRunAtTheSameTimeAndReuseOwnProcess(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			first := newAgentFixture(t, backend)
			first.Prompt = "silent-task"
			second := first
			second.ChatID = chatmodel.NewID()
			runner := newTestRunner(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			finished := make(chan error, 2)
			for _, request := range []AgentRequest{first, second} {
				go func() {
					_, err := runner.Run(ctx, request, nil)
					finished <- err
				}()
			}
			for i := 0; i < 2; i++ {
				if err := <-finished; err != nil {
					t.Fatalf("chat in shared directory could not run: %v", err)
				}
			}
			firstProcess := currentProcess(t, runner, first.ChatID)
			secondProcess := currentProcess(t, runner, second.ChatID)
			if firstProcess.cmd.Process.Pid == secondProcess.cmd.Process.Pid {
				t.Fatal("different chats in the same directory share one CLI process")
			}
			for _, request := range []AgentRequest{first, second} {
				request = nextFixtureTurn(request, "native-session-123")
				request.Prompt = "Continue"
				runFixture(t, runner, request)
			}
			if currentProcess(t, runner, first.ChatID) != firstProcess || currentProcess(t, runner, second.ChatID) != secondProcess {
				t.Fatal("a chat did not reuse its own process")
			}
			if got := fixtureCount(t, first, "spawn"); got != 2 {
				t.Fatalf("started %d CLI processes for two chats, want 2", got)
			}
		})
	}
}

func TestRunnerRejectsConcurrentTurnsAndShutdownStopsActiveProcess(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "block-task"
			runner := newTestRunner(t)
			started := make(chan struct{}, 1)
			finished := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			go func() {
				_, err := runner.Run(ctx, request, func(event AgentEvent) error {
					if event.Type == "delta" {
						started <- struct{}{}
					}
					return nil
				})
				finished <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("fixture did not start")
			}
			process := currentProcess(t, runner, request.ChatID)
			if _, err := runner.Run(ctx, request, nil); err == nil || !strings.Contains(err.Error(), "running turn") {
				t.Fatalf("concurrent turn was accepted: %v", err)
			}
			_ = runner.Close()
			if err := <-finished; !errors.Is(err, context.Canceled) {
				t.Fatalf("active turn not canceled by shutdown: %v", err)
			}
			waitProcessExit(t, process)
			if _, err := runner.Run(ctx, request, nil); err == nil {
				t.Fatal("closed runner accepted new work")
			}
		})
	}
}

func TestProviderFailureDoesNotRestartHealthyCLI(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "failed-turn"
			runner := newTestRunner(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, request, nil)
			if err == nil || !strings.Contains(err.Error(), "fixture failure") {
				t.Fatalf("expected provider failure, got %v", err)
			}
			request = nextFixtureTurn(request, result.SessionID)
			request.Prompt = "Continue"
			runFixture(t, runner, request)
			if fixtureCount(t, request, "spawn") != 1 {
				t.Fatal("healthy process was restarted after a completed failed turn")
			}
		})
	}
}

func TestUnexpectedProcessExitIsNotRetriedAutomatically(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "exit-task"
			runner := newTestRunner(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, request, nil)
			if err == nil || result.Text != "Partial reply" || fixtureCount(t, request, "spawn") != 1 {
				t.Fatalf("unexpected crash handling: result=%#v, error=%v", result, err)
			}
			request = nextFixtureTurn(request, result.SessionID)
			request.Prompt = "Continue"
			runFixture(t, runner, request)
			if fixtureCount(t, request, "spawn") != 2 {
				t.Fatal("new message did not replace crashed process")
			}
		})
	}
}

func TestMissingWorkingDirectoryIsNotCreatedAndDoesNotKeepChatBusy(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.WorkingDirectory += "/invalid!"
	runner := newTestRunner(t)
	if _, err := runner.Run(context.Background(), request, nil); err == nil {
		t.Fatal("missing working directory accepted")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.chats) != 0 {
		t.Fatal("preparation failure left a busy chat entry")
	}
	if _, err := os.Stat(request.WorkingDirectory); !os.IsNotExist(err) {
		t.Fatalf("unexpected working directory creation: %v", err)
	}
}
