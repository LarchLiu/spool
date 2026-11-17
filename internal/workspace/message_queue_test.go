package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestQueuedMessageMovePreservesTheQueue(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendCodex)
	for _, id := range []string{"a", "b", "c", "d"} {
		f.app.restoreQueued(f.chat.ID, &queuedMessage{id: id, input: messageInput{Message: id}})
	}
	// restoreQueued prepends, so begin with d,c,b,a. Relative moves keep newer entries.
	for _, step := range []struct {
		id, before string
		want       []string
	}{
		{"a", "c", []string{"d", "a", "c", "b"}},
		{"d", "b", []string{"a", "c", "d", "b"}},
		{"a", "", []string{"c", "d", "b", "a"}},
		{"d", "d", []string{"c", "d", "b", "a"}},
	} {
		next := f.post(t, "queue", map[string]string{"id": step.id, "action": "move", "before_id": step.before}, 200)
		if got := queuedTexts(next); !slices.Equal(got, step.want) {
			t.Fatalf("move %s before %s: %q, want %q", step.id, step.before, got, step.want)
		}
	}
	for _, body := range []map[string]string{
		{"id": "missing", "action": "move"},
		{"id": "c", "action": "move", "before_id": "missing"},
		{"action": "move"},
	} {
		f.post(t, "queue", body, 409)
	}
	for _, want := range []string{"c", "d", "b", "a"} {
		if next := f.app.takeQueued(f.chat.ID, ""); next == nil || next.input.Message != want {
			t.Fatalf("next queued = %+v, want %s", next, want)
		}
	}
}

func TestQueuedMessageMoveChangesAutomaticSendOrder(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendCodex)
	f.run(t, "steer-wait")
	f.post(t, "messages", map[string]string{"message": "second", "delivery": "queue"}, 202)
	queued := f.post(t, "messages", map[string]string{"message": "third", "delivery": "queue"}, 202)
	moved := f.post(t, "queue", map[string]string{"id": queued.Queued[0].ID, "action": "move"}, 200)
	if got := queuedTexts(moved); !slices.Equal(got, []string{"third", "second"}) {
		t.Fatalf("queue = %q", got)
	}
	f.post(t, "messages", map[string]string{"message": "finish", "delivery": "steer"}, 202)
	done := f.settled(t, 3)
	if got := messageTexts(done); len(got) == 0 || got[0] != "user:second" || len(done.Queued) != 0 {
		t.Fatalf("last turn = %q, queue = %q", got, queuedTexts(done))
	}
}

func TestQueuedMessageEditRestoresOriginalInput(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendCodex)
	data := []byte{0, 255, 42, 13, 10}
	f.app.restoreQueued(f.chat.ID, &queuedMessage{id: "edit", input: messageInput{Message: "  Original\nmessage  ", files: []uploadedAttachment{
		{Attachment: chatmodel.Attachment{Name: "image.png", MediaType: "image/png"}, Data: data},
	}}}, &queuedMessage{id: "keep", input: messageInput{Message: "later"}})
	w := apiRequest(f.app, "POST", "/api/chats/"+f.chat.ID+"/queue", `{"id":"edit","action":"draft"}`, f.cookie, f.csrf)
	var draft queuedDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil || w.Code != 200 {
		t.Fatalf("draft returned %d: %s (%v)", w.Code, w.Body.String(), err)
	}
	if draft.Message != "  Original\nmessage  " || len(draft.Files) != 1 || draft.Files[0].Name != "image.png" || draft.Files[0].MediaType != "image/png" || !bytes.Equal(draft.Files[0].Data, data) {
		t.Fatalf("original input not preserved: %+v", draft)
	}
	if len(f.snapshot().Queued) != 2 {
		t.Fatal("reading a draft removed it before the client was ready")
	}
	edited := f.post(t, "queue", map[string]string{"id": "edit", "action": "edit"}, 200)
	if got := queuedTexts(edited); !slices.Equal(got, []string{"later"}) {
		t.Fatalf("edit removed another entry: %q", got)
	}
	for _, action := range []string{"draft", "edit", "move"} {
		f.post(t, "queue", map[string]string{"id": "edit", "action": action}, 409)
	}
	f.post(t, "queue", map[string]string{"action": "edit"}, 409)
	// A turn can claim the entry while a client is preparing the editor.
	f.app.takeQueued(f.chat.ID, "keep")
	f.post(t, "queue", map[string]string{"id": "keep", "action": "edit"}, 409)
}

// A written steer is owned by the server even after its HTTP wait expires.
func TestSteeringAcknowledgementTimeoutKeepsTheMessage(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendCodex)
	// Agent tests exercise the actual acknowledgement timer with virtual time.
	// Here the provider returns that outcome after writing the real request;
	// this layer checks the HTTP snapshot and the later consumption event.
	f.app.runner = &pendingSteerRunner{CLIRunner: f.app.runner.(*agent.CLIRunner), delivered: func() bool {
		return fixtureCount(t, f.request, "steer") > 0
	}}
	f.run(t, "steer-wait steer-ack-gate")
	waiting := f.post(t, "messages", map[string]string{"message": "change course", "delivery": "steer"}, 202)
	if got := messageTexts(waiting); !slices.Contains(got, "user:change course") || !waiting.Transcript.Parts[len(waiting.Transcript.Parts)-1].Pending {
		t.Fatalf("unacknowledged message is not pending: %q", got)
	}
	f.releaseSteer(t)
	done := f.settled(t, 1)
	if !slices.Contains(messageTexts(done), "user:change course") || len(done.Queued) != 0 {
		t.Fatalf("late acknowledgement lost or duplicated the message: transcript %q, queue %q", messageTexts(done), queuedTexts(done))
	}
}

type pendingSteerRunner struct {
	*agent.CLIRunner
	delivered func() bool
}

func (r *pendingSteerRunner) Steer(ctx context.Context, request agent.SteerRequest) error {
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- r.CLIRunner.Steer(wait, request) }()
	// Cancellation after the fixture receives the request exercises the same
	// pending-delivery contract without waiting for the production timeout.
	// The turn continues on the runner's own context.
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-finished:
			return err
		case <-tick.C:
			if r.delivered() {
				cancel()
				return <-finished
			}
		}
	}
}

func (f *queueFixture) releaseSteer(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.request.WorkingDirectory, "fixture-steer-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledSteeringRequestKeepsItsProviderOutcome(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected", "stopped"} {
		t.Run(outcome, func(t *testing.T) {
			f := newQueueFixture(t, chatmodel.BackendCodex)
			prompt := "steer-wait steer-ack-gate"
			if outcome == "rejected" {
				prompt += " steer-rejected"
			}
			turn := f.run(t, prompt)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := localRequest("POST", "/api/chats/"+f.chat.ID+"/messages", strings.NewReader(`{"message":"change course","delivery":"steer"}`)).WithContext(ctx)
			r.AddCookie(f.cookie)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", f.csrf)
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				f.app.ServeHTTP(w, r)
				finished <- w
			}()
			deadline := time.Now().Add(5 * time.Second)
			for fixtureCount(t, f.request, "steer") == 0 {
				if time.Now().After(deadline) {
					t.Fatal("provider did not receive the steering request")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			select {
			case w := <-finished:
				if w.Code != 202 {
					t.Fatalf("written steering request returned %d: %s", w.Code, w.Body.String())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled HTTP wait did not finish")
			}
			if outcome == "stopped" {
				stopped := f.post(t, "stop", map[string]string{"turn_id": turn.ID}, 200)
				if got := queuedTexts(stopped); !slices.Equal(got, []string{"change course"}) {
					t.Fatalf("stopped unacknowledged message = %q", got)
				}
				return
			}
			f.releaseSteer(t)
			turns := 1
			if outcome == "rejected" {
				turns = 2
			}
			done := f.settled(t, turns)
			if !slices.Contains(messageTexts(done), "user:change course") || len(done.Queued) != 0 {
				t.Fatalf("%s steering lost or duplicated the message: transcript %q, queue %q", outcome, messageTexts(done), queuedTexts(done))
			}
		})
	}
}

type queueFixture struct {
	app     *App
	store   *Store
	chat    chatmodel.Session
	request agent.AgentRequest
	cookie  *http.Cookie
	csrf    string
}

func newQueueFixture(t *testing.T, backend chatmodel.Backend) *queueFixture {
	t.Helper()
	request := newAgentFixture(t, backend)
	store := testStore(t)
	user := testUser(t, store, "alice")
	project, err := store.SaveProject(Project{Name: "Queue", Path: request.WorkingDirectory, Members: []string{user.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.saveAgent(AgentInfo{Backend: backend, Enabled: true, Installed: true, Executable: request.Binary}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: request.Binary})
	app := NewApp(store, runner, "", "")
	t.Cleanup(func() { _ = app.Close() })
	fixture := &queueFixture{app: app, store: store, request: request}
	fixture.cookie, fixture.csrf = loginRequest(t, app, "alice")
	if fixture.chat, err = store.CreateChat(user.ID, project.ID, backend); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *queueFixture) post(t *testing.T, endpoint string, body any, want int) chatmodel.Session {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(f.app, "POST", "/api/chats/"+f.chat.ID+"/"+endpoint, string(encoded), f.cookie, f.csrf)
	if w.Code != want {
		t.Fatalf("%s %s: status %d, want %d: %s", endpoint, encoded, w.Code, want, w.Body.String())
	}
	var chat chatmodel.Session
	_ = json.Unmarshal(w.Body.Bytes(), &chat)
	return chat
}

// run starts a turn and waits until the provider is waiting for more input.
func (f *queueFixture) run(t *testing.T, prompt string) chatmodel.Turn {
	t.Helper()
	f.post(t, "messages", map[string]string{"message": prompt}, 202)
	return waitLiveTurn(t, f.app, f.chat.ID, func(turn chatmodel.Turn) bool { return turn.Reply == "Hello world" })
}

func (f *queueFixture) snapshot() chatmodel.Session {
	chat, _ := f.store.Get(f.chat.ID)
	return f.app.snapshot(chat)
}

// settled waits for the chat to be idle after the provider began that many turns.
func (f *queueFixture) settled(t *testing.T, turns int) chatmodel.Session {
	t.Helper()
	method := "turn/start"
	if f.chat.Backend == chatmodel.BackendClaude {
		method = "user"
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		chat := f.snapshot()
		if chat.Status != chatmodel.StatusRunning && !f.app.turnRunning(f.chat.ID) && fixtureCount(t, f.request, method) == turns {
			return chat
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("chat did not settle after %d turns: %d begun, status %s", turns, fixtureCount(t, f.request, method), f.snapshot().Status)
	return chatmodel.Session{}
}

func queuedTexts(chat chatmodel.Session) []string {
	texts := []string{}
	for _, message := range chat.Queued {
		texts = append(texts, message.Message)
	}
	return texts
}

// messageTexts lists the text a chat's last turn shows, in order.
func messageTexts(chat chatmodel.Session) []string {
	texts := []string{}
	if len(chat.Turns) == 0 {
		return texts
	}
	turn := chat.Turns[len(chat.Turns)-1]
	for _, part := range chat.Transcript.Parts[max(0, turn.PartStart):min(turn.PartEnd, len(chat.Transcript.Parts))] {
		if part.Kind == "text" {
			texts = append(texts, part.Role+":"+part.Text)
		}
	}
	return texts
}

func TestBusyChatQueuesMessagesForTheNextTurn(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			f := newQueueFixture(t, backend)
			f.run(t, "steer-wait")
			queued := f.post(t, "messages", map[string]string{"message": "second", "delivery": "queue"}, 202)
			if got := queuedTexts(queued); !slices.Equal(got, []string{"second"}) || queued.Status != chatmodel.StatusRunning {
				t.Fatalf("queued %q with status %s, want the message held while the turn runs", got, queued.Status)
			}
			queued = f.post(t, "messages", map[string]string{"message": "third", "delivery": "queue"}, 202)
			if got := queuedTexts(queued); !slices.Equal(got, []string{"second", "third"}) {
				t.Fatalf("queue = %q, want messages in the order sent", got)
			}
			if got := queuedTexts(f.snapshot()); !slices.Equal(got, []string{"second", "third"}) {
				t.Fatalf("another viewer sees queue %q", got)
			}
			f.post(t, "messages", map[string]string{"message": "fourth", "delivery": "later"}, 400)
			f.post(t, "queue", map[string]string{"id": queued.Queued[1].ID, "action": "reorder"}, 400)
			f.post(t, "queue", map[string]string{"id": "missing", "action": "steer"}, 409)
			removed := f.post(t, "queue", map[string]string{"id": queued.Queued[1].ID, "action": "delete"}, 200)
			if got := queuedTexts(removed); !slices.Equal(got, []string{"second"}) {
				t.Fatalf("queue after removing a message = %q", got)
			}
			// The turn ends once it has taken a steering message; the queue follows.
			f.post(t, "messages", map[string]string{"message": "change course", "delivery": "steer"}, 202)
			done := f.settled(t, 2)
			if len(done.Queued) != 0 || done.Status != chatmodel.StatusCompleted {
				t.Fatalf("queue %q and status %s after the queued turn", queuedTexts(done), done.Status)
			}
			if got := messageTexts(done); len(got) == 0 || got[0] != "user:second" {
				t.Fatalf("the queued message did not become the next turn: %q", got)
			}
		})
	}
}

func TestSteeringMessageJoinsTheTurnWhereTheModelTookIt(t *testing.T) {
	for _, test := range []struct {
		name    string
		backend chatmodel.Backend
		prompt  string
		want    []string
	}{
		{"codex", chatmodel.BackendCodex, "steer-wait", []string{"user:steer-wait", "assistant:Hello world", "user:change course"}},
		{"claude", chatmodel.BackendClaude, "steer-wait", []string{"user:steer-wait", "assistant:Hello world", "user:change course"}},
		// Claude Code answers a message that came after the last model request
		// in a turn of its own, which still belongs to the turn that ran here.
		{"claude follow-up", chatmodel.BackendClaude, "steer-late", []string{"user:steer-late", "assistant:Hello world", "user:change course", "assistant: Steered"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newQueueFixture(t, test.backend)
			f.run(t, test.prompt)
			f.post(t, "messages", map[string]string{"message": "change course", "delivery": "steer"}, 202)
			done := f.settled(t, 1)
			if got := messageTexts(done); !slices.Equal(got, test.want) {
				t.Fatalf("turn shows %q, want %q", got, test.want)
			}
			if slices.ContainsFunc(done.Transcript.Parts, func(part chatmodel.Part) bool { return part.Pending }) {
				t.Fatal("a message the model took is still marked pending")
			}
			if len(done.Queued) != 0 || done.Status != chatmodel.StatusCompleted {
				t.Fatalf("queue %q and status %s after steering", queuedTexts(done), done.Status)
			}
			var steer map[string]any
			for _, entry := range fixtureLog(t, f.request) {
				if entry["method"] == "steer" {
					steer, _ = entry["value"].(map[string]any)
				}
			}
			if steer == nil {
				t.Fatal("the provider never received the steering message")
			}
		})
	}
}

// A queued message can be handed to the running turn.
func TestQueuedMessageCanSteerTheRunningTurn(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendCodex)
	f.run(t, "steer-wait")
	queued := f.post(t, "messages", map[string]string{"message": "change course", "delivery": "queue"}, 202)
	steered := f.post(t, "queue", map[string]string{"id": queued.Queued[0].ID, "action": "steer"}, 200)
	if len(steered.Queued) != 0 {
		t.Fatalf("a steered message stayed queued: %q", queuedTexts(steered))
	}
	done := f.settled(t, 1)
	if got, want := messageTexts(done), []string{"user:steer-wait", "assistant:Hello world", "user:change course"}; !slices.Equal(got, want) {
		t.Fatalf("turn shows %q, want %q", got, want)
	}
}

// The provider accepting a message is not the model seeing it. Until then the
// message waits after the turn's output, and a turn that stops first gives it
// back rather than losing it.
func TestStoppedTurnReturnsUnseenSteeringToTheQueue(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			f := newQueueFixture(t, backend)
			turn := f.run(t, "steer-hold")
			waiting := f.post(t, "messages", map[string]string{"message": "not yet", "delivery": "steer"}, 202)
			texts := messageTexts(waiting)
			if len(waiting.Queued) != 0 || len(texts) == 0 || texts[len(texts)-1] != "user:not yet" {
				t.Fatalf("accepted message is shown as %q with queue %q, want it after the turn's output", texts, queuedTexts(waiting))
			}
			// Clients keep what a turn has shown them, except what is marked as
			// still able to be withdrawn.
			if parts := waiting.Transcript.Parts; !parts[len(parts)-1].Pending || parts[len(parts)-2].Pending {
				t.Fatalf("only the unseen message may be marked pending: %+v", parts[len(parts)-2:])
			}
			stopped := f.post(t, "stop", map[string]string{"turn_id": turn.ID}, 200)
			if got := queuedTexts(stopped); !slices.Equal(got, []string{"not yet"}) {
				t.Fatalf("queue after stopping = %q, want the unseen message back", got)
			}
			if got := messageTexts(stopped); slices.Contains(got, "user:not yet") {
				t.Fatalf("the stopped turn shows a message its model never saw: %q", got)
			}
			// A failed or stopped turn leaves its queue to the user.
			if got := queuedTexts(f.settled(t, 1)); !slices.Equal(got, []string{"not yet"}) {
				t.Fatalf("queue after the stopped turn ended = %q", got)
			}
			f.post(t, "queue", map[string]string{"id": stopped.Queued[0].ID, "action": "steer"}, 200)
			done := f.settled(t, 2)
			if got := messageTexts(done); len(done.Queued) != 0 || len(got) == 0 || got[0] != "user:not yet" {
				t.Fatalf("sending the returned message: queue %q, turn %q", queuedTexts(done), got)
			}
		})
	}
}

// Claude Code can drop a message it had queued. The model never saw it, so
// it is sent as the next turn instead.
func TestSteeringMessageNoTurnTookIsSentNext(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendClaude)
	f.run(t, "steer-wait steer-cancelled")
	f.post(t, "messages", map[string]string{"message": "never mind", "delivery": "steer"}, 202)
	done := f.settled(t, 2)
	if got := messageTexts(done); len(done.Queued) != 0 || len(got) == 0 || got[0] != "user:never mind" {
		t.Fatalf("dropped message: queue %q, last turn %q", queuedTexts(done), got)
	}
}

// A message no turn could start with would stay at the head of the queue and
// hold back every message behind it, so it is refused when it is sent.
func TestMessageThatCannotStartATurnIsNotQueued(t *testing.T) {
	f := newQueueFixture(t, chatmodel.BackendCodex)
	f.run(t, "steer-wait")
	for _, delivery := range []string{"queue", "steer"} {
		for name, message := range map[string]string{"empty": " \n", "too long": strings.Repeat("x", chatmodel.MaxMessageBytes+1)} {
			f.post(t, "messages", map[string]string{"message": message, "delivery": delivery}, 400)
			if got := queuedTexts(f.snapshot()); len(got) != 0 {
				t.Fatalf("%s message sent to %s was queued (%d)", name, delivery, len(got))
			}
		}
	}
	if steered := fixtureCount(t, f.request, "steer"); steered != 0 {
		t.Fatalf("the provider was handed %d messages it cannot take", steered)
	}
	f.post(t, "messages", map[string]string{"message": "next", "delivery": "queue"}, 202)
	f.post(t, "messages", map[string]string{"message": "change course", "delivery": "steer"}, 202)
	done := f.settled(t, 2)
	if got := messageTexts(done); len(done.Queued) != 0 || len(got) == 0 || got[0] != "user:next" {
		t.Fatalf("queue %q after the refused messages, last turn %q", queuedTexts(done), got)
	}
}

// Clients show adjacent user parts as one message unless their times differ.
// A message that follows the prompt, or another message, with nothing in
// between must still be told apart from it.
func TestSteeringMessageIsToldApartFromTheInputBeforeIt(t *testing.T) {
	prompt := []chatmodel.Part{{Role: "user", Kind: "text", Text: "prompt"}, {Role: "user", Kind: "image", Text: "[image]"}}
	message := func(text string, at int64) []chatmodel.Part {
		return []chatmodel.Part{{Role: "user", Kind: "text", Text: text, Timestamp: at}}
	}
	parts := appendSteering(slices.Clone(prompt), message("first", 2000), 1000)
	// Two messages sent within the same millisecond.
	parts = appendSteering(parts, message("second", 2000), 1000)
	var times []int64
	for _, part := range parts {
		times = append(times, part.Timestamp)
	}
	if want := []int64{1000, 1000, 2000, 2001}; !slices.Equal(times, want) {
		t.Fatalf("times = %v, want %v", times, want)
	}
	// Input that follows output keeps the time it has, or none.
	after := appendSteering([]chatmodel.Part{prompt[0], {Role: "assistant", Kind: "text", Text: "reply"}}, message("later", 2000), 1000)
	if after[0].Timestamp != 0 || after[2].Timestamp != 2000 {
		t.Fatalf("times after output = %d and %d", after[0].Timestamp, after[2].Timestamp)
	}
}

// A provider may record a steering message as a turn of its own. The turn
// that ran here then covers several native turns until they are all written.
func TestCompletedTurnSpansTheNativeTurnsOfItsSteering(t *testing.T) {
	user := func(text string) chatmodel.Part { return chatmodel.Part{Role: "user", Kind: "text", Text: text} }
	reply := func(text string) chatmodel.Part {
		return chatmodel.Part{Role: "assistant", Kind: "text", Text: text}
	}
	earlier := []chatmodel.Part{user("before"), reply("earlier")}
	live := []chatmodel.Part{user("prompt"), reply("first"), user("steer"), reply("second")}
	finished := completedTurn{spans: 2, turn: chatmodel.Turn{ID: "ran-here", Status: chatmodel.StatusCompleted, Prompt: "prompt", Reply: "firstsecond", Parts: live, InputEnd: 1, PartEnd: 4}}
	native := func(turns ...[]chatmodel.Part) chatmodel.Session {
		chat := chatmodel.Session{Transcript: chatmodel.Transcript{Parts: slices.Clone(earlier)}, Turns: []chatmodel.Turn{{ID: "earlier", PartEnd: 2, Parts: earlier}}}
		for i, parts := range turns {
			turn := chatmodel.Turn{ID: "native-follow-up", PartStart: len(chat.Transcript.Parts), Parts: parts}
			if i == 0 {
				turn.ID = "ran-here"
			}
			chat.Transcript.Parts = append(chat.Transcript.Parts, parts...)
			turn.PartEnd = len(chat.Transcript.Parts)
			populateTurnContent(&turn)
			chat.Turns = append(chat.Turns, turn)
		}
		return chat
	}
	for name, test := range map[string]struct {
		chat   chatmodel.Session
		bridge bool
	}{
		"nothing after the prompt is written": {native(live[:1]), true},
		"the further turn is not written":     {native(live[:2]), true},
		"the further turn has no answer":      {native(live[:2], live[2:3]), true},
		"everything is written":               {native(live[:2], live[2:]), false},
	} {
		t.Run(name, func(t *testing.T) {
			merged, used := mergeCompletedTurn(test.chat, finished)
			if used != test.bridge {
				t.Fatalf("bridged = %v, want %v", used, test.bridge)
			}
			if !used {
				if len(merged.Turns) != 3 {
					t.Fatalf("native history was rewritten: %d turns", len(merged.Turns))
				}
				return
			}
			var texts []string
			for _, part := range merged.Transcript.Parts {
				texts = append(texts, part.Text)
			}
			if want := []string{"before", "earlier", "prompt", "first", "steer", "second"}; !slices.Equal(texts, want) {
				t.Fatalf("transcript = %q, want %q", texts, want)
			}
			last := merged.Turns[len(merged.Turns)-1]
			if len(merged.Turns) != 2 || last.ID != "ran-here" || last.PartStart != 2 || last.PartEnd != 6 {
				t.Fatalf("turns after bridging: %#v", merged.Turns)
			}
		})
	}
	// Without steering, only the last native turn can be the one that ran here.
	single := finished
	single.spans = 1
	if _, used := mergeCompletedTurn(native(live[:2], live[2:3]), single); used {
		t.Fatal("a turn without steering was matched to an earlier native turn")
	}
}
