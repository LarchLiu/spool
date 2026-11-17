package workspace

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

const maxQueuedMessages = 20

var errInvalidMessage = errors.New("message must be nonempty and at most 128 KB")

// validateMessage is what any turn requires of its input. A message is held
// to it before it is queued or steered: one that can never start a turn would
// otherwise stay at the head of its queue, in front of every later message.
func validateMessage(prompt string, files int) error {
	if (strings.TrimSpace(prompt) == "" && files == 0) || len(prompt) > chatmodel.MaxMessageBytes {
		return errInvalidMessage
	}
	return nil
}

// A message accepted while its chat was busy. The queue belongs to the chat,
// not to a client, and lives only as long as this process: uploads are held
// in memory until their turn starts.
type queuedMessage struct {
	id        string
	input     messageInput
	options   chatmodel.CodexOptions
	createdAt int64
}

// A message handed to the provider for the running turn. Until the model
// takes it in, it is shown after everything the turn has produced so far.
type steeringMessage struct {
	id    string
	entry *queuedMessage
	parts []chatmodel.Part
	// asking is set until the provider answers. A turn that ends meanwhile
	// sets ended, and leaves the message to the request that is asking.
	asking, ended, requeue bool
	// An acknowledgement still outstanding when the HTTP request returned.
	// Only consumption can retire this message; completion alone cannot.
	unconfirmed bool
}

// The steering of one running turn, guarded by liveMu.
type turnSteering struct {
	turn    string
	pending []*steeringMessage
	taken   int
}

func (a *App) queuedMessages(chatID string) []chatmodel.QueuedMessage {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	queue := a.queues[chatID]
	if len(queue) == 0 {
		return nil
	}
	messages := make([]chatmodel.QueuedMessage, 0, len(queue))
	for _, entry := range queue {
		message := chatmodel.QueuedMessage{ID: entry.id, UserID: entry.input.userID, Message: entry.input.Message, CreatedAt: entry.createdAt}
		for _, file := range entry.input.files {
			message.Attachments = append(message.Attachments, file.Name)
		}
		messages = append(messages, message)
	}
	return messages
}

// queueChanged wakes idle chat streams; running ones poll the queue anyway.
func (a *App) queueChanged(chatID string) {
	if err := a.store.touchSession(chatID); err != nil {
		log.Printf("chat %s: queue revision: %v", chatID, err)
	}
}

// queueWhileRunning holds a message for the end of the running turn. It
// reports false when no turn is running, so the caller starts one instead.
// turnMu orders this against the release of that turn, which drains the queue.
func (a *App) queueWhileRunning(chat chatmodel.Session, input messageInput, options chatmodel.CodexOptions, front bool) (bool, error) {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.activeTurns[chat.ID] == nil {
		return false, nil
	}
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	if len(a.queues[chat.ID]) >= maxQueuedMessages {
		return true, fmt.Errorf("this chat already has %d queued messages", maxQueuedMessages)
	}
	if a.queues == nil {
		a.queues = make(map[string][]*queuedMessage)
	}
	entry := &queuedMessage{id: chatmodel.NewID(), input: input, options: options, createdAt: time.Now().UnixMilli()}
	if front {
		a.queues[chat.ID] = append([]*queuedMessage{entry}, a.queues[chat.ID]...)
	} else {
		a.queues[chat.ID] = append(a.queues[chat.ID], entry)
	}
	return true, nil
}

// takeQueued removes one message, or the oldest when id is empty.
func (a *App) takeQueued(chatID, id string) *queuedMessage {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	queue := a.queues[chatID]
	for i, entry := range queue {
		if id == "" || entry.id == id {
			a.queues[chatID] = append(append([]*queuedMessage(nil), queue[:i]...), queue[i+1:]...)
			if len(a.queues[chatID]) == 0 {
				delete(a.queues, chatID)
			}
			return entry
		}
	}
	return nil
}

// moveQueued changes only the selected entry's position. A concurrent enqueue
// is kept, and a message already taken by a turn cannot be put back by a drag.
func (a *App) moveQueued(chatID, id, before string) bool {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	queue := a.queues[chatID]
	from := slices.IndexFunc(queue, func(entry *queuedMessage) bool { return entry.id == id })
	to := len(queue)
	if before != "" {
		to = slices.IndexFunc(queue, func(entry *queuedMessage) bool { return entry.id == before })
	}
	if from < 0 || to < 0 {
		return false
	}
	if from == to {
		return true
	}
	entry := queue[from]
	queue = slices.Delete(queue, from, from+1)
	if from < to {
		to--
	}
	a.queues[chatID] = slices.Insert(queue, to, entry)
	return true
}

// Read uploads before cancelling an entry, so a client can prepare the editor
// and check its attachment limits without risking the queued message.
type queuedDraftFile struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
}

type queuedDraft struct {
	Message string            `json:"message"`
	Files   []queuedDraftFile `json:"files"`
}

func (a *App) queuedDraft(chatID, id string) (queuedDraft, bool) {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	for _, entry := range a.queues[chatID] {
		if entry.id != id {
			continue
		}
		draft := queuedDraft{Message: entry.input.Message, Files: make([]queuedDraftFile, 0, len(entry.input.files))}
		for _, file := range entry.input.files {
			draft.Files = append(draft.Files, queuedDraftFile{Name: file.Name, MediaType: file.MediaType, Data: file.Data})
		}
		return draft, true
	}
	return queuedDraft{}, false
}

// restoreQueued puts messages back at the head of the queue, in their order.
func (a *App) restoreQueued(chatID string, entries ...*queuedMessage) {
	if len(entries) == 0 {
		return
	}
	a.queueMu.Lock()
	if a.queues == nil {
		a.queues = make(map[string][]*queuedMessage)
	}
	a.queues[chatID] = append(append([]*queuedMessage(nil), entries...), a.queues[chatID]...)
	a.queueMu.Unlock()
}

// startQueued sends a queued message, the oldest when id is empty, as the
// next turn. One that cannot start stays queued to be retried or removed.
func (a *App) startQueued(chatID, id string) error {
	entry := a.takeQueued(chatID, id)
	if entry == nil {
		return nil
	}
	chat, ok := a.store.Get(chatID)
	if !ok {
		return nil
	}
	if err := a.startTurnWithInput(chat, entry.input, entry.options); err != nil {
		a.restoreQueued(chatID, entry)
		return err
	}
	return nil
}

// steerTurn hands a message to the running turn through the provider's own
// steering input. The provider accepting it is not yet the model seeing it:
// the message waits after the turn's output until the provider says which.
func (a *App) steerTurn(ctx context.Context, chat chatmodel.Session, entry *queuedMessage) error {
	steerer, ok := a.runner.(interface {
		Steer(context.Context, agent.SteerRequest) error
	})
	if !ok {
		return agent.ErrNoActiveTurn
	}
	// A goal command is a native control, not a message a model can take mid-turn.
	if command, err := chatmodel.ParseGoalCommand(entry.input.Message); err != nil || command != nil {
		return agent.ErrNoActiveTurn
	}
	files, err := a.materializeAttachments(chat.ID, entry.input.files)
	if err != nil {
		return err
	}
	steer := &steeringMessage{id: agent.NewSteerID(), entry: entry, asking: true, parts: steeringParts(entry.input, files, time.Now().UnixMilli())}
	a.liveMu.Lock()
	current, live := a.live[chat.ID]
	if !live || current.Status != chatmodel.StatusRunning {
		a.liveMu.Unlock()
		return agent.ErrNoActiveTurn
	}
	state := a.steering[chat.ID]
	if state == nil || state.turn != current.ID {
		state = &turnSteering{turn: current.ID}
		if a.steering == nil {
			a.steering = make(map[string]*turnSteering)
		}
		a.steering[chat.ID] = state
	}
	// Registered first: the turn may report the message taken before the
	// provider's answer to this request is read.
	state.pending = append(state.pending, steer)
	a.liveMu.Unlock()
	err = steerer.Steer(ctx, agent.SteerRequest{ID: steer.id, ChatID: chat.ID, Prompt: entry.input.Message, Attachments: files, Files: files})
	a.liveMu.Lock()
	steer.asking = false
	requeued := false
	if errors.Is(err, agent.ErrSteerPending) {
		steer.unconfirmed = true
		err = nil
		if steer.ended && !steer.requeue {
			// The turn ended before acknowledgement. Its request can now
			// queue or start the message through the ordinary idle path.
			err = agent.ErrNoActiveTurn
		}
	}
	switch {
	case err != nil:
		state.pending = slices.DeleteFunc(state.pending, func(pending *steeringMessage) bool { return pending == steer })
	case steer.ended && steer.requeue:
		// The turn stopped or failed with the message still unseen.
		a.restoreQueued(chat.ID, entry)
		requeued = true
	}
	a.liveMu.Unlock()
	if requeued {
		a.queueChanged(chat.ID)
	}
	return err
}

// steeringParts is a steering message as its turn shows it.
func steeringParts(input messageInput, files []chatmodel.AgentAttachment, at int64) []chatmodel.Part {
	var parts []chatmodel.Part
	if strings.TrimSpace(input.Message) != "" {
		parts = append(parts, chatmodel.Part{Role: "user", Kind: "text", Text: input.Message, Timestamp: at})
	}
	for _, file := range files {
		parts = append(parts, chatmodel.Part{Role: "user", Kind: "context", Name: file.Name, Text: file.Name, FilePath: file.Path, Timestamp: at})
	}
	return parts
}

// appendSteering adds a steering message to a turn's parts. Clients show
// adjacent user parts as one message unless their times differ, so the input
// before it is given the time its turn started, and the message a later one.
func appendSteering(parts, message []chatmodel.Part, started int64) []chatmodel.Part {
	last := int64(0)
	for i := len(parts) - 1; i >= 0 && parts[i].Role == "user"; i-- {
		if parts[i].Timestamp == 0 && parts[i].ID == "" {
			parts[i].Timestamp = started
		}
		last = max(last, parts[i].Timestamp)
	}
	at := last + 1
	if len(message) > 0 {
		at = max(at, message[0].Timestamp)
	}
	for _, part := range message {
		part.Timestamp = at
		parts = append(parts, part)
	}
	return parts
}

// withSteeringLocked shows the messages the model has yet to take in after
// the parts of their turn. They are marked pending: a later state of the turn
// may hold one elsewhere, or not at all once it has gone back to the queue,
// and a client must not keep the place it was first shown in. The caller
// holds liveMu.
func (a *App) withSteeringLocked(chatID string, turn chatmodel.Turn) chatmodel.Turn {
	state := a.steering[chatID]
	if state == nil || state.turn != turn.ID || len(state.pending) == 0 {
		return turn
	}
	turn.Parts = ownedParts(turn.Parts)
	settled := len(turn.Parts)
	for _, steer := range state.pending {
		turn.Parts = appendSteering(turn.Parts, steer.parts, turn.StartedAt)
	}
	for i := settled; i < len(turn.Parts); i++ {
		turn.Parts[i].Pending = true
	}
	turn.PartEnd = turn.PartStart + len(turn.Parts)
	return turn
}

// settleSteeringLocked acts on the provider's word about one message: the
// model is about to see it, so it joins the turn where the turn now stands,
// or no turn will take it, so it returns to the head of the queue. It
// reports whether the queue changed. The caller holds liveMu.
func (a *App) settleSteeringLocked(chatID string, turn *chatmodel.Turn, id string, taken bool) bool {
	state := a.steering[chatID]
	if state == nil || state.turn != turn.ID {
		return false
	}
	if taken {
		state.taken++
	}
	at := slices.IndexFunc(state.pending, func(pending *steeringMessage) bool { return pending.id == id })
	if at < 0 {
		return false
	}
	steer := state.pending[at]
	state.pending = slices.Delete(state.pending, at, at+1)
	if taken {
		turn.Parts = appendSteering(turn.Parts, steer.parts, turn.StartedAt)
		return false
	}
	a.restoreQueued(chatID, steer.entry)
	return true
}

// steeredLocked counts the messages the model took in during a turn.
func (a *App) steeredLocked(chatID, turnID string) int {
	if state := a.steering[chatID]; state != nil && state.turn == turnID {
		return state.taken
	}
	return 0
}

// endSteeringLocked closes the steering of a turn that has ended. A completed
// turn took every message its provider acknowledged. An unconfirmed message,
// or an unseen one on a stopped/failed turn, returns to the queue. It reports
// whether the queue changed. The caller holds liveMu.
func (a *App) endSteeringLocked(chatID, turnID string, requeue bool) bool {
	state := a.steering[chatID]
	if state == nil || state.turn != turnID {
		return false
	}
	delete(a.steering, chatID)
	var unseen []*queuedMessage
	for _, steer := range state.pending {
		if steer.asking {
			steer.ended, steer.requeue = true, requeue
		} else if requeue || steer.unconfirmed {
			unseen = append(unseen, steer.entry)
		}
	}
	state.pending = nil
	a.restoreQueued(chatID, unseen...)
	return len(unseen) > 0
}

func (a *App) turnRunning(chatID string) bool {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	return a.activeTurns[chatID] != nil
}

// deliverWhileRunning queues or steers a message sent to a busy chat. It
// reports false when the chat is idle and the message should start a turn.
func (a *App) deliverWhileRunning(ctx context.Context, chat chatmodel.Session, input messageInput, options chatmodel.CodexOptions) (bool, error) {
	if err := validateMessage(input.Message, len(input.files)); err != nil {
		return true, err
	}
	front := false
	if input.Delivery == "steer" && a.turnRunning(chat.ID) {
		err := a.steerTurn(ctx, chat, &queuedMessage{id: chatmodel.NewID(), input: input, options: options, createdAt: time.Now().UnixMilli()})
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, agent.ErrNoActiveTurn) {
			return true, err
		}
		// Not steerable right now: next in line once the turn ends.
		front = true
	}
	queued, err := a.queueWhileRunning(chat, input, options, front)
	if queued && err == nil {
		a.queueChanged(chat.ID)
	}
	return queued, err
}

// queueResponse steers, sends or removes one queued message.
func (a *App) queueResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, user string) {
	var input struct {
		ID       string `json:"id"`
		Action   string `json:"action"`
		BeforeID string `json:"before_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	respond := func() {
		latest, _ := a.store.Get(chat.ID)
		allowed := a.store.CanControlChat(user, chat.ID)
		latest.CanControl = &allowed
		apiJSON(w, 200, transportWindow(r, a.snapshot(latest)))
	}
	switch input.Action {
	case "draft":
		draft, ok := a.queuedDraft(chat.ID, input.ID)
		if !ok {
			apiError(w, 409, "This message is no longer queued")
			return
		}
		apiJSON(w, 200, draft)
	case "edit":
		if input.ID == "" || a.takeQueued(chat.ID, input.ID) == nil {
			apiError(w, 409, "This message is no longer queued")
			return
		}
		a.queueChanged(chat.ID)
		respond()
	case "move":
		if !a.moveQueued(chat.ID, input.ID, input.BeforeID) {
			apiError(w, 409, "This message is no longer queued")
			return
		}
		a.queueChanged(chat.ID)
		respond()
	case "delete":
		if a.takeQueued(chat.ID, input.ID) != nil {
			a.queueChanged(chat.ID)
		}
		respond()
	case "steer":
		entry := a.takeQueued(chat.ID, input.ID)
		if entry == nil {
			apiError(w, 409, "This message is no longer queued")
			return
		}
		var err error
		if a.turnRunning(chat.ID) {
			err = a.steerTurn(r.Context(), chat, entry)
		} else {
			err = a.startTurnWithInput(chat, entry.input, entry.options)
		}
		if err != nil {
			a.restoreQueued(chat.ID, entry)
			if errors.Is(err, agent.ErrNoActiveTurn) {
				apiError(w, 409, "The running turn cannot take this message yet")
			} else {
				apiError(w, 409, err.Error())
			}
			return
		}
		a.queueChanged(chat.ID)
		respond()
	default:
		apiError(w, 400, "Invalid queue action")
	}
}
