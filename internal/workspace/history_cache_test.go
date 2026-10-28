package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func countNativeHistoryReads(store *Store) (*atomic.Int64, *atomic.Int64) {
	indexes, reads := &atomic.Int64{}, &atomic.Int64{}
	store.historyCache.index = func(backend chatmodel.Backend, path string, start, end int64) (agent.NativeSessionIndex, error) {
		indexes.Add(1)
		return agent.IndexSessionTo(backend, path, start, end)
	}
	store.historyCache.readTurn = func(backend chatmodel.Backend, path string, turn agent.NativeTurn) (chatmodel.Transcript, error) {
		reads.Add(1)
		return agent.ReadSessionTurn(backend, path, turn)
	}
	return indexes, reads
}

func TestNativeHistoryCacheSharesConcurrentViewers(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 25, 0)
	before, _ := os.ReadFile(path)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for viewer := range 20 {
		wait.Go(func() {
			<-start
			chat := chatmodel.Session{ID: fmt.Sprintf("viewer-chat-%d", viewer), Backend: chatmodel.BackendCodex, NativeSessionID: "same-native", NativeSessionPath: path}
			snapshot, err := store.readHistory(chat)
			if err != nil {
				t.Errorf("viewer history: %v", err)
				return
			}
			if len(snapshot.Turns) != 25 || snapshot.Turns[0].ID != historyTurnID(chat.ID, snapshot.Turns[0].NativeTurnID) {
				t.Error("shared cache lost history or reused another chat's turn identity")
				return
			}
			// Mutating a response cannot poison another viewer's cached parts.
			snapshot.Transcript.Parts[0].Text = "viewer-only change"
			snapshot.Turns[0].Parts[0].Text = "viewer-only turn change"
		})
	}
	close(start)
	wait.Wait()
	if indexes.Load() != 1 || reads.Load() != 25 {
		t.Fatalf("history was decoded repeatedly: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
	page, err := store.readHistory(chatmodel.Session{ID: "fresh-chat", Backend: chatmodel.BackendCodex, NativeSessionID: "same-native", NativeSessionPath: path})
	if err != nil || page.Turns[0].Prompt != "prompt-000" || page.Transcript.Parts[0].Text != "prompt-000" {
		t.Fatal("response mutation corrupted shared history", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("cache modified the native file")
	}
	assertNoTurnTables(t, store)
}

func TestNativeHistoryCacheReplacesChangedAndDeletedFiles(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	chat := chatmodel.Session{ID: "cached-chat", Backend: chatmodel.BackendCodex, NativeSessionID: "cached-native", NativeSessionPath: path}
	check := func(turns int, reply string) {
		t.Helper()
		page, err := store.readHistory(chat)
		if err != nil || len(page.Turns) != turns || page.Turns[0].Reply != reply {
			t.Fatalf("stale or invalid history: turns=%d error=%v", len(page.Turns), err)
		}
	}
	check(1, "answer-000")
	check(1, "answer-000")
	if indexes.Load() != 1 || reads.Load() != 1 {
		t.Fatal("unchanged file did not hit cache")
	}
	appendNativeTurns(t, path, 1, 1, 0)
	check(2, "answer-000")
	if indexes.Load() != 2 || reads.Load() != 3 {
		t.Fatal("append did not replace the entire old generation")
	}
	data, _ := os.ReadFile(path)
	data = bytes.ReplaceAll(data, []byte("answer-000"), []byte("edited-000"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 123)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	check(2, "edited-000")
	if indexes.Load() != 3 {
		t.Fatal("same-size edit did not invalidate cache")
	}
	// Replacement must invalidate even if size and modification time are copied.
	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	data = bytes.ReplaceAll(data, []byte("edited-000"), []byte("reused-000"))
	if err := os.WriteFile(replacement, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	check(2, "reused-000")
	if indexes.Load() != 4 {
		t.Fatal("file identity was not checked")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.readHistory(chat); !os.IsNotExist(err) {
		t.Fatal("deleted source served stale cached history", err)
	}
	if len(store.historyCache.files) != 0 || store.historyCache.bytes != 0 {
		t.Fatal("deleted source retained its cache")
	}
	appendNativeTurns(t, path, 5, 1, 0)
	check(1, "answer-005")
}

func TestNativeHistoryCacheDoesNotPublishStaleInflightConversion(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	chat := chatmodel.Session{ID: "cached-chat", Backend: chatmodel.BackendCodex, NativeSessionID: "cached-native", NativeSessionPath: path}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var reads atomic.Int64
	store.historyCache.readTurn = func(backend chatmodel.Backend, path string, turn agent.NativeTurn) (chatmodel.Transcript, error) {
		transcript, err := agent.ReadSessionTurn(backend, path, turn)
		if reads.Add(1) == 1 {
			close(started)
			<-release
		}
		return transcript, err
	}
	type result struct {
		chat chatmodel.Session
		err  error
	}
	done := make(chan result, 1)
	go func() {
		page, err := store.readHistory(chat)
		done <- result{page, err}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("conversion never started")
	}
	data, _ := os.ReadFile(path)
	data = bytes.ReplaceAll(data, []byte("answer-000"), []byte("edited-000"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	newer, err := store.readHistory(chat)
	if err != nil || newer.Turns[0].Reply != "edited-000" {
		t.Fatal("new generation waited for or reused the old conversion", err)
	}
	unblock()
	select {
	case older := <-done:
		if older.err != nil || older.chat.Turns[0].Reply != "edited-000" {
			t.Fatal("in-flight conversion returned stale content", older.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight conversion did not finish")
	}
	_, _ = store.readHistory(chat)
	if reads.Load() != 2 {
		t.Fatal("old conversion evicted or overwrote the newer cache", reads.Load())
	}
}

func TestNativeHistoryCacheBoundsMemoryAndEvictsLeastRecent(t *testing.T) {
	store := testStore(t)
	store.historyCache.maxFiles = 2
	store.historyCache.setMaxBytes(2048)
	indexes, reads := countNativeHistoryReads(store)
	root := t.TempDir()
	chat := chatmodel.Session{ID: "cache-lru", Backend: chatmodel.BackendCodex, NativeSessionID: "cache-lru-native"}
	paths := []string{filepath.Join(root, "a.jsonl"), filepath.Join(root, "b.jsonl"), filepath.Join(root, "c.jsonl")}
	for _, path := range paths {
		appendNativeTurns(t, path, 0, 1, 0)
	}
	load := func(path string) {
		t.Helper()
		chat.NativeSessionPath = path
		if _, err := store.readHistory(chat); err != nil {
			t.Fatal(err)
		}
		if store.historyCache.bytes > 2048 || len(store.historyCache.files) > 2 {
			t.Fatal("cache grew beyond its bounds")
		}
	}
	load(paths[0])
	load(paths[1])
	load(paths[0])
	load(paths[2])
	load(paths[0])
	if indexes.Load() != 3 {
		t.Fatal("frequently viewed file was evicted")
	}
	load(paths[1])
	if indexes.Load() != 4 || reads.Load() <= 3 {
		t.Fatal("least recently used file was not evicted")
	}
	store.historyCache.clear()
	if store.historyCache.bytes != 0 || len(store.historyCache.files) != 0 {
		t.Fatal("cache cleanup retained history")
	}
	// A complete turn can exceed the target, but must still be cached intact.
	store.historyCache.setMaxBytes(1024)
	var oversizedReads int
	store.historyCache.readTurn = func(chatmodel.Backend, string, agent.NativeTurn) (chatmodel.Transcript, error) {
		oversizedReads++
		return chatmodel.Transcript{Parts: []chatmodel.Part{{Role: "assistant", Kind: "text", Text: strings.Repeat("x", 2000)}}}, nil
	}
	for _, path := range []string{paths[0], paths[0], paths[1], paths[1], paths[0]} {
		chat.NativeSessionPath = path
		page, err := store.readHistory(chat)
		if err != nil || page.Transcript.Parts[0].Text != strings.Repeat("x", 2000) || len(store.historyCache.files) != 1 || store.historyCache.bytes < 2000 {
			t.Fatal("oversized response was not cached in full", err)
		}
	}
	if oversizedReads != 3 {
		t.Fatal("oversized turn cache did not reuse or evict whole turns", oversizedReads)
	}
}

func TestNativeHistoryCacheUnrelatedFilesDoNotBlock(t *testing.T) {
	store := testStore(t)
	root := t.TempDir()
	blockedPath, otherPath := filepath.Join(root, "blocked.jsonl"), filepath.Join(root, "other.jsonl")
	appendNativeTurns(t, blockedPath, 0, 1, 0)
	appendNativeTurns(t, otherPath, 0, 1, 0)
	started, release := make(chan struct{}), make(chan struct{})
	store.historyCache.readTurn = func(backend chatmodel.Backend, path string, turn agent.NativeTurn) (chatmodel.Transcript, error) {
		if path == blockedPath {
			close(started)
			<-release
		}
		return agent.ReadSessionTurn(backend, path, turn)
	}
	load := func(path string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := store.readHistory(chatmodel.Session{ID: path, Backend: chatmodel.BackendCodex, NativeSessionPath: path})
			done <- err
		}()
		return done
	}
	blocked := load(blockedPath)
	defer func() {
		close(release)
		select {
		case err := <-blocked:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("blocked reader did not finish")
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first file parse did not start")
	}
	select {
	case err := <-load(otherPath):
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unrelated file waited for the blocked parse")
	}
}

func TestNativeHistoryCacheSharesClaudeParts(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	records := []map[string]any{
		{"type": "user", "uuid": "user", "promptId": "prompt", "message": map[string]any{"content": "question"}},
		{"type": "assistant", "uuid": "answer", "parentUuid": "user", "message": map[string]any{"model": "claude-test", "content": []any{map[string]any{"type": "text", "text": "reply"}}}},
	}
	var data bytes.Buffer
	for _, record := range records {
		if err := json.NewEncoder(&data).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"viewer-one", "viewer-two"} {
		page, err := store.readHistory(chatmodel.Session{ID: id, Backend: chatmodel.BackendClaude, NativeSessionPath: path})
		if err != nil || len(page.Turns) != 1 || page.Turns[0].Prompt != "question" || page.Turns[0].Reply != "reply" || page.Turns[0].Model != "claude-test" {
			t.Fatalf("invalid cached Claude history: %+v, %v", page.Turns, err)
		}
	}
	if indexes.Load() != 1 || reads.Load() != 1 {
		t.Fatal("Claude history was parsed repeatedly")
	}
}

func TestNativeHistoryCacheRetainsOversizedSessionAsAWhole(t *testing.T) {
	store := testStore(t)
	store.historyCache.setMaxBytes(1024)
	_, reads := countNativeHistoryReads(store)
	root := t.TempDir()
	first, second := filepath.Join(root, "first.jsonl"), filepath.Join(root, "second.jsonl")
	for _, path := range []string{first, second} {
		appendNativeTurns(t, path, 0, 3, 1)
	}
	load := func(path string) {
		t.Helper()
		snapshot, err := store.readHistory(chatmodel.Session{ID: "whole-session", Backend: chatmodel.BackendCodex, NativeSessionPath: path})
		if err != nil || len(snapshot.Turns) != 3 || len(snapshot.Transcript.Parts) != 9 || snapshot.Turns[0].Prompt != "prompt-000" || snapshot.Turns[2].Reply != "answer-002" {
			t.Fatal("oversized session lost content", err)
		}
		if len(store.historyCache.files) != 1 || store.historyCache.bytes < 24000 {
			t.Fatal("oversized session was not cached intact")
		}
	}
	load(first)
	load(first)
	if reads.Load() != 3 {
		t.Fatal("oversized session evicted its own early turns")
	}
	load(second)
	load(second)
	load(first)
	if reads.Load() != 9 {
		t.Fatal("cache did not reuse and evict whole sessions", reads.Load())
	}
}

func TestNativeHistoryCacheDoesNotPublishPartialDecode(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 3, 0)
	decodeErr := errors.New("failed to decode turn")
	store.historyCache.readTurn = func(backend chatmodel.Backend, path string, turn agent.NativeTurn) (chatmodel.Transcript, error) {
		if turn.ID == "native-turn-001" {
			return chatmodel.Transcript{}, decodeErr
		}
		return agent.ReadSessionTurn(backend, path, turn)
	}
	chat := chatmodel.Session{ID: "partial", Backend: chatmodel.BackendCodex, NativeSessionPath: path}
	snapshot, err := store.readHistory(chat)
	if !errors.Is(err, decodeErr) || len(snapshot.Turns) != 0 || len(snapshot.Transcript.Parts) != 0 || store.historyCache.bytes != 0 {
		t.Fatal("failed parse returned or cached partial history", err)
	}
	_, reads := countNativeHistoryReads(store)
	snapshot, err = store.readHistory(chat)
	if err != nil || len(snapshot.Turns) != 3 || reads.Load() != 3 {
		t.Fatal("retry did not fully parse the session", err)
	}
}

func TestNativeHistoryCacheChangingSourceDoesNotReturnPartialHistory(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	var reads int
	store.historyCache.readTurn = func(backend chatmodel.Backend, path string, turn agent.NativeTurn) (chatmodel.Transcript, error) {
		transcript, err := agent.ReadSessionTurn(backend, path, turn)
		reads++
		stamp := time.Unix(1700000000+int64(reads), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return transcript, err
	}
	page, err := store.readHistory(chatmodel.Session{ID: "unstable", Backend: chatmodel.BackendCodex, NativeSessionPath: path})
	if !errors.Is(err, errHistoryChanged) || reads != 3 || len(page.Turns) != 0 || len(page.Transcript.Parts) != 0 {
		t.Fatalf("unstable source returned stale or partial content: turns=%d reads=%d error=%v", len(page.Turns), reads, err)
	}
	if len(store.historyCache.files) != 0 || store.historyCache.bytes != 0 {
		t.Fatal("unstable source retained stale cached content")
	}
}
