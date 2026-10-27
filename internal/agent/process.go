package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	chatmodel "spool/internal/model"
)

const (
	defaultAgentIdleTimeout = 15 * time.Minute
	agentExitGracePeriod    = 5 * time.Second
)

type CLIRunner struct {
	// IdleTimeout only applies between completed turns, never to running tasks.
	IdleTimeout      time.Duration
	mu               sync.Mutex
	chats            map[string]*chatProcess
	closed           bool
	runs             sync.WaitGroup
	processes        sync.WaitGroup
	catalogMu        sync.Mutex
	catalog          chatmodel.CodexCatalog
	catalogKey       string
	catalogUntil     time.Time
	catalogErr       error
	catalogCancel    context.CancelFunc
	binaryOverrides  map[chatmodel.Backend]string
	permissionMu     sync.Mutex
	permissions      map[string]permissionCacheEntry
	permissionCancel context.CancelFunc
}

type chatProcess struct {
	backend   chatmodel.Backend
	binary    string
	directory string
	ctx       context.Context
	cancel    context.CancelFunc
	process   *agentProcess
	busy      bool
	timer     *time.Timer
	deadline  time.Time
}

func NewCLIRunner() *CLIRunner {
	return &CLIRunner{IdleTimeout: defaultAgentIdleTimeout, chats: make(map[string]*chatProcess)}
}

func (r *CLIRunner) reserve(request AgentRequest) (*chatProcess, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("agent runner is closed")
	}
	if r.chats == nil {
		r.chats = make(map[string]*chatProcess)
	}
	chat := r.chats[request.ChatID]
	if chat != nil {
		if chat.busy {
			// A stopped turn may still be unwinding its provider process. Detach
			// it so a follow-up message can start immediately; the old release
			// path will not remove the replacement from the map.
			if chat.ctx.Err() == nil {
				return nil, fmt.Errorf("chat already has a running turn")
			}
			if r.chats[request.ChatID] == chat {
				delete(r.chats, request.ChatID)
			}
			chat = nil
		}
		if chat != nil && chat.timer != nil {
			chat.timer.Stop()
		}
		dead := false
		if chat != nil && chat.process != nil {
			select {
			case <-chat.process.done:
				dead = true
			default:
			}
		}
		if chat != nil && (dead || chat.ctx.Err() != nil || chat.backend != request.Backend || chat.binary != request.Binary || chat.directory != request.WorkingDirectory) {
			chat.cancel()
			chat = nil
		}
	}
	if chat == nil {
		ctx, cancel := context.WithCancel(context.Background())
		chat = &chatProcess{backend: request.Backend, binary: request.Binary, directory: request.WorkingDirectory, ctx: ctx, cancel: cancel}
		r.chats[request.ChatID] = chat
	}
	chat.busy = true
	r.runs.Add(1)
	return chat, nil
}

// Stop cancels the persistent provider process for a chat. It is deliberately
// separate from Run's request context so the web stop action can terminate a
// turn without waiting for the CLI to finish reaping its children.
func (r *CLIRunner) Stop(chatID string) {
	r.mu.Lock()
	chat := r.chats[chatID]
	r.mu.Unlock()
	if chat != nil {
		chat.cancel()
	}
}

func (r *CLIRunner) release(chatID string, chat *chatProcess, reusable bool) {
	r.mu.Lock()
	chat.busy = false
	if chat.process != nil {
		select {
		case <-chat.process.done:
			reusable = false
		default:
		}
	}
	if r.closed || !reusable || chat.ctx.Err() != nil {
		if r.chats[chatID] == chat {
			delete(r.chats, chatID)
		}
		chat.cancel()
	} else {
		idle := r.IdleTimeout
		if idle <= 0 {
			idle = defaultAgentIdleTimeout
		}
		chat.deadline = time.Now().Add(idle)
		chat.timer = time.AfterFunc(idle, func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			// A stopped timer callback may already be waiting for the lock.
			if r.chats[chatID] == chat && !chat.busy && !time.Now().Before(chat.deadline) {
				delete(r.chats, chatID)
				chat.cancel()
			}
		})
	}
	stopped := chat.ctx.Err() != nil
	r.mu.Unlock()
	if stopped && chat.process != nil {
		<-chat.process.done
	}
	r.runs.Done()
}

func (r *CLIRunner) Run(ctx context.Context, request AgentRequest, emit func(AgentEvent) error) (AgentResult, error) {
	if (len(request.Prompt) == 0 && len(request.Attachments) == 0) || len(request.Prompt) > chatmodel.MaxMessageBytes {
		return AgentResult{}, fmt.Errorf("message must be nonempty and at most 128 KB")
	}
	if !request.Backend.Valid() {
		return AgentResult{}, fmt.Errorf("unsupported agent backend")
	}
	if err := chatmodel.ValidatePermissionMode(request.Backend, request.PermissionMode); err != nil {
		return AgentResult{}, err
	}
	if !chatmodel.ValidID(request.ChatID) {
		return AgentResult{}, fmt.Errorf("invalid chat ID")
	}
	if err := chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: request.Model, ReasoningEffort: request.ReasoningEffort}); err != nil {
		return AgentResult{}, err
	}
	if request.Backend == chatmodel.BackendClaude {
		if err := chatmodel.ValidateClaudeEffort(request.ReasoningEffort); err != nil {
			return AgentResult{}, err
		}
	}
	if request.NativeSessionID != "" && !chatmodel.ValidID(request.NativeSessionID) {
		return AgentResult{}, fmt.Errorf("invalid provider session ID")
	}
	if err := ctx.Err(); err != nil {
		return AgentResult{}, contextError(ctx)
	}
	if emit == nil {
		emit = func(AgentEvent) error { return nil }
	}
	directory, err := WorkingDirectory(request.WorkingDirectory)
	if err != nil {
		return AgentResult{}, err
	}
	request.WorkingDirectory = directory
	chat, err := r.reserve(request)
	if err != nil {
		return AgentResult{}, err
	}
	reusable := false
	defer func() { r.release(request.ChatID, chat, reusable) }()
	if chat.process == nil {
		process, err := startAgentProcess(chat.ctx, request)
		if err != nil {
			return AgentResult{}, err
		}
		r.mu.Lock()
		chat.process = process
		r.processes.Add(1)
		r.mu.Unlock()
		go func() {
			defer r.processes.Done()
			<-process.done
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.chats[request.ChatID] == chat && !chat.busy {
				if chat.timer != nil {
					chat.timer.Stop()
				}
				delete(r.chats, request.ChatID)
				chat.cancel()
			}
		}()
	}
	// Request cancellation still stops a turn; it must not tie an idle process
	// to the HTTP request after that request has completed successfully.
	stop := context.AfterFunc(ctx, chat.cancel)
	defer stop()
	var result AgentResult
	if request.Backend == chatmodel.BackendClaude {
		result, err, reusable = chat.process.runClaude(ctx, request, emit)
	} else {
		result, err, reusable = chat.process.runCodex(ctx, request, emit)
	}
	if ctx.Err() != nil {
		return result, contextError(ctx)
	}
	return result, err
}

func (r *CLIRunner) Close() error {
	r.mu.Lock()
	r.closed = true
	if r.catalogCancel != nil {
		r.catalogCancel()
	}
	if r.permissionCancel != nil {
		r.permissionCancel()
	}
	for _, chat := range r.chats {
		if chat.timer != nil {
			chat.timer.Stop()
		}
		chat.cancel()
	}
	r.chats = make(map[string]*chatProcess)
	r.mu.Unlock()
	r.runs.Wait()
	// Include processes already removed by an idle timer but still being reaped.
	r.processes.Wait()
	return nil
}

// Each provider has its own protocol state, but shares the pipe transport.
// Only one turn owns these fields at a time; process termination is concurrent.
type agentProcess struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	messages       chan map[string]any
	done           chan struct{}
	ctx            context.Context
	stderr         synchronizedBuffer
	waitErr        error
	initialized    bool
	sessionID      string
	model          string
	selectedModel  string
	effort         string
	nextID         int
	queued         []map[string]any
	permissionMode string
	approve        chatmodel.ApprovalHandler
}

func startAgentProcess(ctx context.Context, request AgentRequest) (*agentProcess, error) {
	args := []string{"app-server", "--listen", "stdio://", "--config", "sandbox_workspace_write.network_access=false"}
	if request.Backend == chatmodel.BackendClaude {
		args = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
			"--include-partial-messages", "--permission-mode", "acceptEdits", "--permission-prompts", "host", "--allow-dangerously-skip-permissions"}
		if request.NativeSessionID != "" {
			args = append(args, "--resume", request.NativeSessionID)
		}
	}
	cmd := exec.CommandContext(ctx, request.Binary, args...)
	configureAgentCommand(cmd)
	cmd.WaitDelay = agentExitGracePeriod
	cmd.Dir = request.WorkingDirectory
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	process := &agentProcess{cmd: cmd, stdin: stdin, messages: make(chan map[string]any, 256), done: make(chan struct{}), ctx: ctx}
	process.stderr.buffer.limit = maxStderrBytes
	cmd.Stdout = writer
	cmd.Stderr = &process.stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("start %s CLI: %w", request.Backend.Label(), err)
	}
	readDone := make(chan error, 1)
	go func() {
		defer close(process.messages)
		defer reader.Close()
		scanner := newScanner(reader)
		for scanner.Scan() {
			var message map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				continue
			}
			select {
			case process.messages <- message:
			case <-ctx.Done():
				readDone <- ctx.Err()
				return
			}
		}
		if scanner.Err() != nil {
			// A malformed/oversized frame cannot leave a process stuck on stdout.
			if cmd.Cancel != nil {
				_ = cmd.Cancel()
			} else {
				_ = cmd.Process.Kill()
			}
		}
		readDone <- scanner.Err()
	}()
	go func() {
		waitErr := cmd.Wait()
		_ = writer.Close()
		readErr := <-readDone
		// Reap tool children even when the parent CLI exits on its own.
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		} else {
			_ = cmd.Process.Kill()
		}
		_ = stdin.Close()
		process.waitErr = errors.Join(waitErr, readErr)
		close(process.done)
	}()
	return process, nil
}

func (p *agentProcess) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(p.stdin, "%s\n", data)
	return err
}

func (p *agentProcess) read(ctx context.Context) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, contextError(ctx)
	case <-p.ctx.Done():
		return nil, contextError(p.ctx)
	case value, ok := <-p.messages:
		if ok {
			return value, nil
		}
		<-p.done
		if p.waitErr != nil {
			return nil, processError("Agent CLI", p.waitErr, p.stderr.String())
		}
		return nil, fmt.Errorf("agent CLI exited before completing its turn")
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer limitedBuffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
