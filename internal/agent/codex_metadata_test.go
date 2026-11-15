package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type metadataReader struct {
	io.ReaderAt
	bytes int64
	first int64
}

func (r *metadataReader) ReadAt(data []byte, offset int64) (int, error) {
	if r.first < 0 || offset < r.first {
		r.first = offset
	}
	n, err := r.ReaderAt.ReadAt(data, offset)
	r.bytes += int64(n)
	return n, err
}

func metadataTurn(i int) CodexHistoryTurn {
	return CodexHistoryTurn{ID: fmt.Sprintf("turn-%04d", i), StartedAt: int64(i+1) * 1000}
}

func metadataRecord(t *testing.T, i int, model string) []byte {
	t.Helper()
	effort := "effort"
	if i%2 != 0 {
		effort = "reasoning_effort"
	}
	data, err := json.Marshal(map[string]any{"type": "turn_context", "timestamp": time.UnixMilli(metadataTurn(i).StartedAt).UTC().Format(time.RFC3339Nano), "payload": map[string]any{
		"turn_id": metadataTurn(i).ID, "model": model, effort: "high",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestCodexMetadataReadsPagesAndOnlyAppendedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	output := []byte(`{"type":"response_item","payload":{"output":"` + strings.Repeat("x", 8000) + `"}}` + "\n")
	for i := range 1000 {
		if _, err := file.Write(append(metadataRecord(t, i, fmt.Sprintf("model-%d", i)), output...)); err != nil {
			t.Fatal(err)
		}
	}
	c := &CodexMetadata{}
	reader := &metadataReader{ReaderAt: file, first: -1}
	read := func(first, count int) {
		t.Helper()
		turns := make([]CodexHistoryTurn, count)
		for i := range turns {
			turns[i] = metadataTurn(first + i)
		}
		info, _ := file.Stat()
		metadata, err := c.read(reader, info, turns)
		if err != nil || len(metadata) != count {
			t.Fatalf("page metadata: count=%d error=%v", len(metadata), err)
		}
		for i := first; i < first+count; i++ {
			if turn := metadata[metadataTurn(i).ID]; turn.Model != fmt.Sprintf("model-%d", i) || turn.ReasoningEffort != "high" {
				t.Fatalf("turn %d: %+v", i, turn)
			}
		}
	}
	read(990, 10)
	if reader.bytes > 200_000 || reader.first < 7_000_000 {
		t.Fatalf("latest page read old history: bytes=%d first=%d", reader.bytes, reader.first)
	}
	previous := reader.bytes
	read(990, 10)
	if reader.bytes != previous {
		t.Fatal("unchanged page performed file reads")
	}
	reader.bytes, reader.first = 0, -1
	read(980, 10)
	if reader.bytes > 200_000 || reader.first < 7_000_000 {
		t.Fatalf("older page restarted from the beginning: bytes=%d first=%d", reader.bytes, reader.first)
	}
	info, _ := file.Stat()
	oldSize := info.Size()
	if _, err := file.Write(append(metadataRecord(t, 1000, "model-1000"), output...)); err != nil {
		t.Fatal(err)
	}
	reader.bytes, reader.first = 0, -1
	read(990, 11)
	if reader.bytes > 10_000 || reader.first < oldSize-128 {
		t.Fatalf("append replayed completed data: bytes=%d first=%d oldSize=%d", reader.bytes, reader.first, oldSize)
	}
	// An incomplete context must be retried from its start when completed.
	partial := metadataRecord(t, 1001, "model-1001")
	if _, err := file.Write(partial[:len(partial)/2]); err != nil {
		t.Fatal(err)
	}
	info, _ = file.Stat()
	metadata, err := c.read(reader, info, []CodexHistoryTurn{metadataTurn(1001)})
	if err != nil || len(metadata) != 0 {
		t.Fatalf("partial context was accepted: %+v %v", metadata, err)
	}
	if _, err := file.Write(partial[len(partial)/2:]); err != nil {
		t.Fatal(err)
	}
	reader.bytes, reader.first = 0, -1
	read(1001, 1)
	if reader.bytes > 1000 || reader.first < oldSize {
		t.Fatalf("completion replayed earlier turns: bytes=%d first=%d", reader.bytes, reader.first)
	}
}

func TestCodexMetadataInvalidatesChangedAndReplacedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write := func(model string) {
		t.Helper()
		if err := os.WriteFile(path, metadataRecord(t, 0, model), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := &CodexMetadata{}
	check := func(model string) {
		t.Helper()
		got, err := c.Read(path, []CodexHistoryTurn{metadataTurn(0)})
		if err != nil || got[metadataTurn(0).ID].Model != model {
			t.Fatalf("stale metadata: %+v %v, want %q", got, err, model)
		}
	}
	write("model-a")
	check("model-a")
	write("model-b")
	stamp := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	check("model-b")
	// A growing rewrite must not be mistaken for an append.
	write("model-c-with-a-longer-name")
	check("model-c-with-a-longer-name")
	write("small")
	check("small")
	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	if err := os.WriteFile(replacement, metadataRecord(t, 0, "other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	check("other")
}

func TestCodexMetadataMissingNewestIDDoesNotScanHistory(t *testing.T) {
	data := append(bytes.Repeat([]byte("{\"type\":\"unrelated\"}\n"), 100_000), metadataRecord(t, 0, "model-0")...)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	reader := &metadataReader{ReaderAt: file, first: -1}
	c := &CodexMetadata{}
	got, err := c.read(reader, info, []CodexHistoryTurn{metadataTurn(1)})
	if err != nil || len(got) != 0 || reader.bytes > 100_000 {
		t.Fatalf("missing ID scanned history: result=%+v bytes=%d error=%v", got, reader.bytes, err)
	}
}

func TestCodexMetadataContinuesBoundedSearchAcrossLargeToolRecord(t *testing.T) {
	data := append(metadataRecord(t, 0, "model-0"), []byte(`{"type":"response_item","payload":{"output":"`+strings.Repeat("x", 6*1024*1024)+`"}}`+"\n")...)
	data = append(data, metadataRecord(t, 1, "model-1")...)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	reader := &metadataReader{ReaderAt: file, first: -1}
	c := &CodexMetadata{}
	got, err := c.read(reader, info, []CodexHistoryTurn{metadataTurn(0)})
	if err != nil || len(got) != 0 || reader.bytes > codexMetadataBudget+1000 {
		t.Fatalf("search exceeded budget: result=%+v bytes=%d error=%v", got, reader.bytes, err)
	}
	reader.bytes, reader.first = 0, -1
	got, err = c.read(reader, info, []CodexHistoryTurn{metadataTurn(0)})
	if err != nil || got[metadataTurn(0).ID].Model != "model-0" || reader.bytes > 3*1024*1024 {
		t.Fatalf("search did not resume: result=%+v bytes=%d error=%v", got, reader.bytes, err)
	}
}

func TestCodexMetadataRetriesInitialPartialLineAndUpdatesSameTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	first, second := metadataRecord(t, 0, "model-0"), metadataRecord(t, 1, "model-1")
	if err := os.WriteFile(path, append(first, second[:len(second)/2]...), 0600); err != nil {
		t.Fatal(err)
	}
	c := &CodexMetadata{}
	requested := []CodexHistoryTurn{metadataTurn(0), metadataTurn(1)}
	got, err := c.Read(path, requested)
	if err != nil || len(got) != 1 || got[requested[0].ID].Model != "model-0" {
		t.Fatalf("initial partial file: %+v %v", got, err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(second[len(second)/2:]); err != nil {
		t.Fatal(err)
	}
	got, err = c.Read(path, requested)
	if err != nil || len(got) != 2 || got[requested[1].ID].Model != "model-1" {
		t.Fatalf("completed initial partial file: %+v %v", got, err)
	}
	updated := metadataRecord(t, 1, "updated-model")
	updated = bytes.ReplaceAll(updated, []byte(`"high"`), []byte(`"low"`))
	if _, err := file.Write(updated); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 20 {
		wait.Go(func() {
			got, err := c.Read(path, requested)
			if err != nil || got[requested[1].ID].Model != "updated-model" || got[requested[1].ID].ReasoningEffort != "low" {
				t.Errorf("updated turn/shared reader: %+v %v", got, err)
			}
			// Response maps must not share mutable cache containers.
			got[requested[1].ID] = CodexTurnContext{Model: "viewer-only"}
		})
	}
	wait.Wait()
}

func TestCodexMetadataContextMarkerAcrossBlocks(t *testing.T) {
	for split := 1; split < len(`"turn_context"`); split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			context := []byte(`{"payload":{"turn_id":"turn-0000","model":"large-context","instructions":"` + strings.Repeat("a", 2*codexMetadataBlock) + `"},"type":"turn_context"}` + "\n")
			marker := bytes.Index(context, []byte(`"turn_context"`))
			padding := (codexMetadataBlock - (len(context)-marker-split)%codexMetadataBlock) % codexMetadataBlock
			data := append(context, []byte(strings.Repeat("\n", padding))...)
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := (&CodexMetadata{}).Read(path, []CodexHistoryTurn{metadataTurn(0)})
			if err != nil || got[metadataTurn(0).ID].Model != "large-context" {
				t.Fatalf("split marker missed context: %+v %v", got, err)
			}
		})
	}
}

func TestCodexTurnAfterSeeksToSavedOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	prefix := bytes.Repeat([]byte("{\"type\":\"unrelated\"}\n"), 100_000)
	data := append(prefix, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"next-turn"}}`+"\n")...)
	data = append(data, metadataRecord(t, 1, "next-model")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := CodexTurnAfter(path, int64(len(prefix)))
	if err != nil || got != "next-turn" {
		t.Fatalf("offset recovery: %q %v", got, err)
	}
}

func TestCodexTurnIdentityIsTheOneTheRolloutRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	prefix := metadataRecord(t, 0, "earlier")
	data := append(append([]byte{}, prefix...), []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"other-turn"}}`+"\n")...)
	data = append(data, metadataRecord(t, 1, "announced")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	after := int64(len(prefix))
	for _, tc := range []struct {
		name, announced, want string
		offset                int64
	}{
		{"announced turn recorded after another one", metadataTurn(1).ID, metadataTurn(1).ID, after},
		{"unannounced turn", "", "other-turn", after},
		{"announced under a name the rollout never uses", "live-only-id", "other-turn", after},
		{"announced as a turn written before the request", metadataTurn(0).ID, "other-turn", after},
		{"announced turn found from the start of the rollout", "other-turn", "other-turn", 0},
		{"nothing written yet", metadataTurn(1).ID, "", int64(len(data))},
		{"rollout shorter than the saved offset", "", "", int64(len(data)) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := CodexTurnIdentity(path, tc.offset, tc.announced); err != nil || got != tc.want {
				t.Fatalf("identity %q, want %q (%v)", got, tc.want, err)
			}
		})
	}
}

func TestCodexMetadataKeepsItsResultsWhileTheRolloutGrows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, metadataRecord(t, 0, "model-0"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer file.Close()
		// An active turn appends all the time, also between a read's two stats.
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := file.Write([]byte("{\"type\":\"unrelated\"}\n")); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	c := &CodexMetadata{}
	requested := []CodexHistoryTurn{metadataTurn(0)}
	for range 200 {
		if got, err := c.Read(path, requested); err != nil || got[requested[0].ID].Model != "model-0" {
			close(stop)
			<-done
			t.Fatalf("a growing rollout lost its metadata: %+v %v", got, err)
		}
	}
	close(stop)
	<-done
}

func TestCodexMetadataContinuesAnUnfinishedLongRecordWhereItStoppedSearching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// The newest record is still being written and is longer than one search.
	data := append(metadataRecord(t, 0, "model-0"), []byte(`{"type":"response_item","payload":{"output":"`+strings.Repeat("x", 6*1024*1024))...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	reader := &metadataReader{ReaderAt: file, first: -1}
	c := &CodexMetadata{}
	requested := []CodexHistoryTurn{metadataTurn(1)}
	if got, err := c.read(reader, info, requested); err != nil || len(got) != 0 {
		t.Fatalf("unwritten turn: %+v %v", got, err)
	}
	searched := reader.first
	writer, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Write(append([]byte(`"}}`+"\n"), metadataRecord(t, 1, "model-1")...)); err != nil {
		t.Fatal(err)
	}
	info, _ = file.Stat()
	reader.bytes, reader.first = 0, -1
	got, err := c.read(reader, info, requested)
	if err != nil || got[requested[0].ID].Model != "model-1" {
		t.Fatalf("appended turn: %+v %v", got, err)
	}
	if reader.first < searched {
		t.Fatalf("the append was read from offset %d, before the %d already searched", reader.first, searched)
	}
}
