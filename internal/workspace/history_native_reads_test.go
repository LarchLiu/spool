package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// selectiveCodexHistory lets one thread's read wait, or some threads fail,
// while Codex answers for the others.
type selectiveCodexHistory struct {
	*fakeCodexHistory
	slow             string
	entered, release chan struct{}
	failing          map[string]error
}

func (f *selectiveCodexHistory) Thread(ctx context.Context, id string) (agent.CodexThread, error) {
	if id == f.slow {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		<-f.release
	}
	if err := f.failing[id]; err != nil {
		f.threads++
		return agent.CodexThread{}, err
	}
	return f.fakeCodexHistory.Thread(ctx, id)
}

// nativeCodexThread adds a chat on its own Codex thread to a store whose
// reader is already set.
func nativeCodexThread(t *testing.T, store *Store, thread string) (chatmodel.Session, string) {
	t.Helper()
	chat, err := store.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendNativeTurns(t, path, 0, 2, 1)
	if err := store.SetNativeSessionPath(chat.ID, thread, path); err != nil {
		t.Fatal(err)
	}
	return chat, path
}

func readNatively(chat chatmodel.Session) bool {
	return chat.History != nil && chat.History.Native
}

func TestSlowCodexReadDelaysNoOtherChat(t *testing.T) {
	store := testStore(t)
	fake := &selectiveCodexHistory{fakeCodexHistory: &fakeCodexHistory{count: 3, turnBytes: 10}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	ready, _ := nativeCodexChat(t, store, fake)
	if latest, _ := store.Get(ready.ID); len(latest.Turns) != 3 || !readNatively(latest) {
		t.Fatalf("native window: %d turns %+v", len(latest.Turns), latest.History)
	}
	slow, _ := nativeCodexThread(t, store, "slow-thread")
	fake.slow = "slow-thread"
	started := make(chan error, 1)
	go func() {
		_, err := store.beginTurn(slow.ID, "input", chatmodel.BackendCodex)
		started <- err
	}()
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		close(fake.release)
		t.Fatal("the slow thread was never read")
	}
	// Codex is now answering for the slow thread, and has not finished.
	others := make(chan error, 1)
	go func() {
		if latest, ok := store.Get(ready.ID); !ok || len(latest.Turns) != 3 || !readNatively(latest) {
			others <- errors.New("another chat's cached window was not served")
			return
		}
		if _, err := store.beginTurn(ready.ID, "input", chatmodel.BackendCodex); err != nil {
			others <- err
			return
		}
		_, err := store.createSession("", "", chatmodel.BackendCodex)
		others <- err
	}()
	select {
	case err := <-others:
		if err != nil {
			close(fake.release)
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(fake.release)
		t.Fatal("reading one thread from Codex blocked the other chats")
	}
	close(fake.release)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
}

func TestCodexWindowReadAtTheLastWriteIsReadOnceMoreWhenQuiet(t *testing.T) {
	store := testStore(t)
	store.codexHistory.settle = time.Millisecond
	fake := &fakeCodexHistory{count: 3, turnBytes: 10}
	chat, path := nativeCodexChat(t, store, fake)
	// The rollout's last write is not older than the read that follows it.
	written := time.Now().Add(200 * time.Millisecond)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	if latest, _ := store.Get(chat.ID); len(latest.Turns) != 3 || !readNatively(latest) {
		t.Fatalf("first window: %d turns", len(latest.Turns))
	}
	// Codex makes that write readable without touching the rollout again.
	fake.count = 4
	time.Sleep(time.Until(written) + 20*time.Millisecond)
	if latest, _ := store.Get(chat.ID); len(latest.Turns) != 4 {
		t.Fatalf("a window read at the last write was kept for good: %d turns", len(latest.Turns))
	}
	threads, pages := fake.threads, fake.pages
	if latest, _ := store.Get(chat.ID); len(latest.Turns) != 4 || fake.threads != threads || fake.pages != pages {
		t.Fatalf("a settled window was read again: threads %d→%d", threads, fake.threads)
	}
}

func TestCodexThreadFailureLeavesOtherThreadsOnNativeHistory(t *testing.T) {
	store := testStore(t)
	fake := &selectiveCodexHistory{fakeCodexHistory: &fakeCodexHistory{count: 3, turnBytes: 10}, failing: map[string]error{"broken-thread": errors.New("thread not found")}}
	healthy, path := nativeCodexChat(t, store, fake)
	broken, _ := nativeCodexThread(t, store, "broken-thread")
	if latest, _ := store.Get(broken.ID); len(latest.Turns) != 2 || latest.History != nil {
		t.Fatalf("a thread Codex cannot read did not use its rollout: %d turns %+v", len(latest.Turns), latest.History)
	}
	// That says nothing about the next thread.
	if latest, _ := store.Get(healthy.ID); len(latest.Turns) != 3 || !readNatively(latest) {
		t.Fatalf("one failing thread took native history from another: %d turns %+v", len(latest.Turns), latest.History)
	}
	threads := fake.threads
	if _, _ = store.Get(broken.ID); fake.threads != threads {
		t.Fatal("the failing thread was asked again before its retry")
	}
	// Codex itself not starting stops every new native read until the retry.
	unreachable, _ := nativeCodexThread(t, store, "unreachable-thread")
	fake.failing["unreachable-thread"] = fmt.Errorf("%w: exec: codex: not found", agent.ErrCodexHistoryUnavailable)
	if latest, _ := store.Get(unreachable.ID); len(latest.Turns) != 2 || latest.History != nil {
		t.Fatalf("unreachable Codex: %d turns %+v", len(latest.Turns), latest.History)
	}
	if latest, _ := store.Get(healthy.ID); len(latest.Turns) != 3 || !readNatively(latest) {
		t.Fatal("a window already read was dropped while Codex is unreachable")
	}
	appendNativeTurns(t, path, 2, 1, 1)
	threads = fake.threads
	if latest, _ := store.Get(healthy.ID); len(latest.Turns) != 3 || latest.History != nil || latest.Turns[2].NativeTurnID != "native-turn-002" || fake.threads != threads {
		t.Fatalf("an unreachable Codex was asked again: %d turns %+v threads %d→%d", len(latest.Turns), latest.History, threads, fake.threads)
	}
}

func TestNativeWindowPagesContinueFromCodexCursor(t *testing.T) {
	store := testStore(t)
	fake := &fakeCodexHistory{count: 45, turnBytes: 60_000}
	chat, _ := nativeCodexChat(t, store, fake)
	latest, _ := store.Get(chat.ID)
	if len(latest.Turns) != codexWindowTurnPage || !strings.HasPrefix(latest.History.Before, "native-") {
		t.Fatalf("window: %d turns %+v", len(latest.Turns), latest.History)
	}
	// A viewer pages back through the loaded window, as beside a live turn.
	page, err := historyPage(latest, "after-"+historyCursor(chat.ID, latest.Turns[5]))
	if err != nil {
		t.Fatal(err)
	}
	for range len(latest.Turns) {
		if page.Turns[0].NativeTurnID == latest.Turns[0].NativeTurnID {
			break
		}
		if page, err = historyPage(latest, page.History.Before); err != nil {
			t.Fatalf("cursor inside the window: %v", err)
		}
	}
	// Before the window's first turn only Codex can continue.
	if page.Turns[0].NativeTurnID != "turn-035" || !page.History.HasMore || page.History.Before != latest.History.Before {
		t.Fatalf("first loaded page: first %s history %+v", page.Turns[0].NativeTurnID, page.History)
	}
	older, err := store.codexHistoryPage(context.Background(), latest, page.History.Before)
	if err != nil || len(older.Turns) == 0 || older.Turns[len(older.Turns)-1].NativeTurnID != "turn-034" {
		t.Fatalf("continuing before the window: %v %d turns", err, len(older.Turns))
	}
}

func TestStoppedTurnKeepsTheCursorOfTheWindowItStartedFrom(t *testing.T) {
	store := testStore(t)
	fake := &fakeCodexHistory{count: 45, turnBytes: 60_000}
	chat, path := nativeCodexChat(t, store, fake)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	base, _ := store.Get(chat.ID)
	turn, err := store.beginTurn(chat.ID, "new input", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	app.seedLiveTurn(base, turn)
	// The thread moves on while the turn runs: a newer window, another cursor.
	fake.count = 47
	appendNativeTurns(t, path, 2, 1, 1)
	newer, _ := store.Get(chat.ID)
	if newer.History == nil || newer.History.Before == base.History.Before {
		t.Fatalf("the newer window has the same cursor: %+v", newer.History)
	}
	stopped, err := app.stopTurn(newer, turn.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.Turns) != 11 || stopped.Turns[0].NativeTurnID != "turn-035" || stopped.Turns[10].ID != turn.ID {
		t.Fatalf("stopped turns: %d, first %s", len(stopped.Turns), stopped.Turns[0].NativeTurnID)
	}
	if stopped.History == nil || stopped.History.Before != base.History.Before || !stopped.History.HasMore {
		t.Fatalf("the stop response pages from another window than its turns: %+v", stopped.History)
	}
}

func TestNativeActorIdentityIsBoundOnceAndSurvivesAnUnreadableRollout(t *testing.T) {
	store := testStore(t)
	alice, bob := testUser(t, store, "bound-alice"), testUser(t, store, "bound-bob")
	project := testProject(t, store, alice, bob)
	chat, err := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	if err := store.SetNativeSessionPath(chat.ID, "actor-bound", path); err != nil {
		t.Fatal(err)
	}
	store.codexHistory.reader = &fakeCodexHistory{count: 1, idPrefix: "native-"}
	turn, err := store.beginTurnBy(chat.ID, "follow-up", chat.Backend, "", nil, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendNativeTurns(t, path, 1, 1, 0)
	// Crash before a native turn event binds Bob's request.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.codexHistory.reader = &fakeCodexHistory{count: 2, idPrefix: "native-"}
	bound := func() string {
		t.Helper()
		var identity string
		if err := reopened.db.QueryRow(`SELECT native_turn_id FROM turn_actors WHERE session_id=? AND turn_id=?`, chat.ID, turn.ID).Scan(&identity); err != nil {
			t.Fatal(err)
		}
		return identity
	}
	sender := func() string {
		t.Helper()
		latest, ok := reopened.Get(chat.ID)
		if !ok || len(latest.Turns) != 2 || !readNatively(latest) || latest.History.Error != "" {
			t.Fatalf("native history: found=%v turns=%d history=%+v", ok, len(latest.Turns), latest.History)
		}
		return latest.Turns[1].UserID
	}
	unreadable := os.Geteuid() != 0
	if unreadable {
		// The request cannot be located right now. The history Codex returned is
		// still shown, with the chat's owner as the unresolved sender.
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if user := sender(); user != alice.ID || bound() != "" {
			t.Fatalf("unreadable rollout: sender %s bound %q", user, bound())
		}
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if user := sender(); user != bob.ID {
		t.Fatalf("recovered sender %s, want %s", user, bob.ID)
	}
	if bound() != "native-turn-001" {
		t.Fatalf("the recovered identity was not saved: %q", bound())
	}
	if unreadable {
		// Once bound, the request is found without searching the rollout.
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0600) })
		if user := sender(); user != bob.ID {
			t.Fatalf("bound sender %s, want %s", user, bob.ID)
		}
	}
}

func TestExternalFileReadsTheRolloutOnlyWhenTheWindowCannotDecide(t *testing.T) {
	store := testStore(t)
	owner := testUser(t, store, "native-reference-owner")
	project := testProject(t, store, owner)
	project.AllowExternalFiles = true
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, err := store.CreateChat(owner.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	report := writeSessionFile(t, directory, "report.txt", "report body")
	unrelated := writeSessionFile(t, directory, "unrelated.txt", "never mentioned")
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendNativeTurns(t, path, 0, 2, 1)
	if err := store.SetNativeSessionPath(chat.ID, "native-reference-thread", path); err != nil {
		t.Fatal(err)
	}
	store.codexHistory.reader = &fakeCodexHistory{count: 45, turnBytes: 60_000, note: " wrote " + report}
	// Locating the session's directory indexes the rollout; only deciding from
	// the whole history decodes its bodies.
	_, reads := countNativeHistoryReads(store)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	cookie, _ := loginRequest(t, app, owner.Username)
	get := func(file string) (int, string) {
		w := apiRequest(app, "GET", "/api/chats/"+chat.ID+"/file?path="+url.QueryEscape(file)+"&download=1", "", cookie, "")
		return w.Code, w.Body.String()
	}
	// The loaded turns name the file: nothing else needs reading.
	if code, body := get(report); code != 200 || body != "report body" {
		t.Fatalf("file referenced in the window: %d %s", code, body)
	}
	if reads.Load() != 0 {
		t.Fatalf("a reference in the loaded window still read the rollout's bodies: reads=%d", reads.Load())
	}
	// They do not name this one, so the whole rollout decides, and denies it.
	if code, body := get(unrelated); code != 403 {
		t.Fatalf("unreferenced file: %d %s", code, body)
	}
	if reads.Load() == 0 {
		t.Fatal("an unreferenced file was denied without consulting the rollout")
	}
	if os.Geteuid() == 0 {
		return
	}
	// A rollout that cannot be read decides nothing; that is not a denial.
	appendNativeTurns(t, path, 2, 1, 1)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if code, body := get(unrelated); code != 503 {
		t.Fatalf("unreadable rollout: %d %s", code, body)
	}
	if code, body := get(report); code != 200 || body != "report body" {
		t.Fatalf("file referenced in the window, rollout unreadable: %d %s", code, body)
	}
}

func TestChatStreamEncodesFramesWhileEventsRewriteTheLiveTurn(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "live-viewer")
	project := testProject(t, store, user)
	chat, _ := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	turn, err := store.BeginTurn(chat.ID, "live input")
	if err != nil {
		t.Fatal(err)
	}
	turn = app.seedLiveTurn(chat, turn)
	appendTurnText(&turn, chatmodel.Part{ID: "thought", Role: "assistant", Kind: "thinking", Text: "streamed"})
	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Text: "initial"})
	app.liveMu.Lock()
	app.live[chat.ID] = turn
	app.liveMu.Unlock()
	app.streamHeartbeat = 250 * time.Millisecond
	const steps = 400
	cookie, _ := loginRequest(t, app, user.Username)
	ctx, cancel := context.WithCancel(context.Background())
	request := localRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	written := make(chan struct{})
	go func() {
		defer close(written)
		for i := range steps {
			// What a provider event does: grow the newest part, or restate an
			// earlier one where it is.
			app.liveMu.Lock()
			current := app.live[chat.ID]
			appendTurnText(&current, chatmodel.Part{Role: "assistant", Kind: "text", Text: "x"})
			if i%20 == 0 {
				appendTurnText(&current, chatmodel.Part{ID: "thought", Role: "assistant", Kind: "thinking", Text: fmt.Sprint("restated ", i), Replace: true})
			}
			app.live[chat.ID] = current
			app.liveMu.Unlock()
			time.Sleep(200 * time.Microsecond)
		}
	}()
	defer func() { <-written }()
	want := "initial" + strings.Repeat("x", steps)
	for range 200 {
		_, frame := readChatStreamFrame(t, stream)
		for _, part := range frame.Transcript.Parts {
			if part.Role == "assistant" && part.Kind == "text" && part.Text == want {
				return
			}
		}
	}
	t.Fatal("the stream never delivered the complete live reply")
}
