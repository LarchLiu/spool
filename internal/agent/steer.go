package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

// ErrNoActiveTurn reports that the provider has no running turn that can take
// a steering message. The message was not delivered and may start a new turn.
var ErrNoActiveTurn = errors.New("no active turn to steer")

// ErrSteerPending means the request was written, but its provider has not
// answered yet. The runner still owns the request and will report consumption
// or rejection through the running turn. Callers must keep the message.
var ErrSteerPending = errors.New("steering acknowledgement pending")

// SteerRequest adds a user message to the turn a chat is already running.
type SteerRequest struct {
	// ID names the message in the steered event sent once the model is about
	// to see it. It must be a UUID; one is made when it is empty.
	ID          string
	ChatID      string
	Prompt      string
	Attachments []chatmodel.AgentAttachment
	Files       []chatmodel.AgentAttachment
}

// A steering message the provider accepted but has not yet shown the model.
type pendingSteer struct{ id, text string }

// A turn/steer request Codex has yet to answer.
type steerWaiter struct {
	reply    chan map[string]any
	steer    pendingSteer
	detached bool
}

// NewSteerID returns an identifier fit for SteerRequest.ID.
func NewSteerID() string { return newUUID() }

// Steer hands a message to the provider's own mid-turn input: Codex
// turn/steer, or a Claude Code user message with priority "next". Accepting
// it is not yet showing it to the model; the turn reports that as a steered
// event.
func (r *CLIRunner) Steer(ctx context.Context, request SteerRequest) error {
	if err := ctx.Err(); err != nil {
		return contextError(ctx)
	}
	if chatmodel.ParseSlashCommand(request.Prompt) != nil {
		return fmt.Errorf("Wait for the running response before using agent commands")
	}
	if (strings.TrimSpace(request.Prompt) == "" && len(request.Attachments) == 0) || len(request.Prompt) > chatmodel.MaxMessageBytes {
		return fmt.Errorf("message must be nonempty and at most 128 KB")
	}
	if request.ID == "" {
		request.ID = newUUID()
	}
	r.mu.Lock()
	chat := r.chats[request.ChatID]
	var process *agentProcess
	var backend chatmodel.Backend
	if chat != nil && chat.busy && chat.ctx.Err() == nil {
		process, backend = chat.process, chat.backend
	}
	r.mu.Unlock()
	if process == nil {
		return ErrNoActiveTurn
	}
	input := AgentRequest{Prompt: request.Prompt, Attachments: request.Attachments, Files: request.Files}
	if backend == chatmodel.BackendClaude {
		return process.steerClaude(request.ID, input)
	}
	return process.steerCodex(ctx, request.ID, input)
}

func codexUserInput(request AgentRequest) []any {
	input := []any{map[string]any{"type": "text", "text": agentTextInput(request), "text_elements": []any{}}}
	for _, file := range request.Attachments {
		if file.Image() {
			input = append(input, map[string]any{"type": "localImage", "path": file.Path})
		}
	}
	return input
}

func claudeUserContent(request AgentRequest) (any, error) {
	if len(request.Attachments) == 0 {
		return agentTextInput(request), nil
	}
	parts := []any{map[string]any{"type": "text", "text": agentTextInput(request)}}
	for _, file := range request.Attachments {
		if !file.Image() {
			continue
		}
		data, err := os.ReadFile(file.Path)
		if err != nil {
			return nil, err
		}
		parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": file.MediaType, "data": base64.StdEncoding.EncodeToString(data)}})
	}
	return parts, nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// beginSteering opens the window in which the running turn takes messages.
func (p *agentProcess) beginSteering(turnID string) {
	p.steerMu.Lock()
	p.steerActive, p.steerTurn, p.steerThread, p.steerPending, p.steerRejected = true, turnID, p.sessionID, nil, nil
	p.steerMu.Unlock()
}

func (p *agentProcess) endSteering() {
	p.steerMu.Lock()
	p.steerActive, p.steerTurn, p.steerPending = false, "", nil
	for id, waiter := range p.steerWaiters {
		select {
		case waiter.reply <- map[string]any{"error": map[string]any{"message": ErrNoActiveTurn.Error()}}:
		default:
		}
		delete(p.steerWaiters, id)
	}
	p.steerMu.Unlock()
}

func (p *agentProcess) steerCodex(ctx context.Context, steerID string, request AgentRequest) error {
	p.steerMu.Lock()
	if !p.steerActive || p.steerTurn == "" {
		p.steerMu.Unlock()
		return ErrNoActiveTurn
	}
	p.steerSeq++
	id := fmt.Sprintf("spool-steer-%d", p.steerSeq)
	waiter := steerWaiter{reply: make(chan map[string]any, 1), steer: pendingSteer{id: steerID, text: agentTextInput(request)}}
	if p.steerWaiters == nil {
		p.steerWaiters = make(map[string]steerWaiter)
	}
	p.steerWaiters[id] = waiter
	params := map[string]any{"threadId": p.steerThread, "expectedTurnId": p.steerTurn, "clientUserMessageId": steerID, "input": codexUserInput(request)}
	p.steerMu.Unlock()
	forget := func() {
		p.steerMu.Lock()
		delete(p.steerWaiters, id)
		p.steerMu.Unlock()
	}
	if err := p.write(map[string]any{"id": id, "method": "turn/steer", "params": params}); err != nil {
		forget()
		return err
	}
	// Once written, HTTP cancellation or a slow acknowledgement cannot undo
	// delivery. Leave correlation with the turn's reader, which still handles
	// a late success or rejection. Resolve an already-arrived reply under the
	// same lock so a timeout cannot overtake a definitive answer.
	detach := func() (map[string]any, error) {
		p.steerMu.Lock()
		defer p.steerMu.Unlock()
		if current, waiting := p.steerWaiters[id]; waiting {
			current.detached = true
			p.steerWaiters[id] = current
			return nil, ErrSteerPending
		}
		return <-waiter.reply, nil
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var value map[string]any
	select {
	case value = <-waiter.reply:
	case <-ctx.Done():
		var err error
		if value, err = detach(); err != nil {
			return err
		}
	case <-p.done:
		forget()
		return ErrNoActiveTurn
	case <-timer.C:
		var err error
		if value, err = detach(); err != nil {
			return err
		}
	}
	rpcError, failed := value["error"].(map[string]any)
	if !failed {
		return nil
	}
	message := stringValue(rpcError["message"])
	if strings.Contains(message, "no active turn") {
		return ErrNoActiveTurn
	}
	return fmt.Errorf("Codex turn/steer: %s", chatmodel.Clip(message, maxStderrBytes))
}

// steerResponse routes a turn/steer reply to its caller. The turn's own
// reader calls it, so a reply is never mistaken for a turn event, and a
// message is known as accepted before the item that carries it arrives.
func (p *agentProcess) steerResponse(value map[string]any) bool {
	id, ok := value["id"].(string)
	if !ok || value["method"] != nil || !strings.HasPrefix(id, "spool-steer-") {
		return false
	}
	p.steerMu.Lock()
	if waiter, waiting := p.steerWaiters[id]; waiting {
		_, failed := value["error"].(map[string]any)
		if !failed && p.steerActive {
			p.steerPending = append(p.steerPending, waiter.steer)
		}
		if failed && waiter.detached {
			p.steerRejected = append(p.steerRejected, waiter.steer.id)
		}
		waiter.reply <- value
		delete(p.steerWaiters, id)
	}
	p.steerMu.Unlock()
	return true
}

// Rejections whose HTTP caller has already returned belong to the turn's
// event stream, including responses read while an approval is open.
func (p *agentProcess) rejectedSteers() []string {
	p.steerMu.Lock()
	defer p.steerMu.Unlock()
	ids := p.steerRejected
	p.steerRejected = nil
	return ids
}

// codexSteerTaken matches a user message item of the running turn to the
// steering messages it carries: by the ID sent with one, or else by its text.
// Codex adds the item when the model is about to see them.
func (p *agentProcess) codexSteerTaken(item map[string]any) []string {
	client := stringValue(item["clientId"])
	var texts []string
	content, _ := item["content"].([]any)
	for _, value := range content {
		if block, _ := value.(map[string]any); block["type"] == "text" {
			texts = append(texts, stringValue(block["text"]))
		}
	}
	p.steerMu.Lock()
	defer p.steerMu.Unlock()
	var taken []string
	waiting := p.steerPending[:0]
	for _, pending := range p.steerPending {
		at := slices.Index(texts, pending.text)
		if (client != "" && pending.id == client) || at >= 0 {
			taken = append(taken, pending.id)
			if at >= 0 {
				texts = slices.Delete(texts, at, at+1)
			}
			continue
		}
		waiting = append(waiting, pending)
	}
	p.steerPending = waiting
	return taken
}

func (p *agentProcess) steerClaude(steerID string, request AgentRequest) error {
	content, err := claudeUserContent(request)
	if err != nil {
		return err
	}
	// Writing under the lock orders this message against the turn's decision,
	// on its result, that nothing more is coming.
	p.steerMu.Lock()
	defer p.steerMu.Unlock()
	if !p.steerActive {
		return ErrNoActiveTurn
	}
	if err := p.write(map[string]any{"type": "user", "session_id": p.steerThread, "uuid": steerID, "priority": "next",
		"message": map[string]any{"role": "user", "content": content}, "parent_tool_use_id": nil}); err != nil {
		return err
	}
	p.steerPending = append(p.steerPending, pendingSteer{id: steerID})
	return nil
}

// claudeSteerLifecycle settles a steering message and names it. Claude Code
// reports "started" when the message drains into a turn. Any other state past
// "queued" for a message still waiting is its end: no turn will take it.
func (p *agentProcess) claudeSteerLifecycle(value map[string]any) (id string, started bool, remaining int) {
	command, state := stringValue(value["command_uuid"]), stringValue(value["state"])
	p.steerMu.Lock()
	defer p.steerMu.Unlock()
	if state != "queued" {
		for i, pending := range p.steerPending {
			if pending.id == command {
				p.steerPending = append(p.steerPending[:i], p.steerPending[i+1:]...)
				id, started = command, state == "started"
				break
			}
		}
	}
	return id, started, len(p.steerPending)
}

// claudeTurnSettled reports whether a result ends the work: Claude Code runs
// one more turn for a steering message the finished turn did not take in.
func (p *agentProcess) claudeTurnSettled() bool {
	p.steerMu.Lock()
	defer p.steerMu.Unlock()
	if len(p.steerPending) > 0 {
		return false
	}
	p.steerActive = false
	return true
}
