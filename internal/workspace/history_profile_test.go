package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type historyProfileSample struct {
	Milliseconds float64 `json:"milliseconds"`
	Allocated    uint64  `json:"allocated_bytes"`
}

type historyProfileWriter struct{ bytes int }

func (w *historyProfileWriter) Write(data []byte) (int, error) {
	w.bytes += len(data)
	return len(data), nil
}

// Opt-in, read-only profiling of a user-selected provider transcript. Only
// timings/counts are logged; native content is never copied into the report.
func TestNativeHistoryProfile(t *testing.T) {
	path := os.Getenv("SPOOL_HISTORY_PROFILE")
	if path == "" {
		t.Skip("set SPOOL_HISTORY_PROFILE to an explicitly selected native transcript")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	var indexTime, readTime time.Duration
	store.historyCache.index = func(backend chatmodel.Backend, path string, start, end int64) (agent.NativeSessionIndex, error) {
		began := time.Now()
		value, err := agent.IndexSessionTo(backend, path, start, end)
		indexTime += time.Since(began)
		return value, err
	}
	store.historyCache.readTurn = func(backend chatmodel.Backend, path string, turn agent.NativeTurn) (chatmodel.Transcript, error) {
		began := time.Now()
		value, err := agent.ReadSessionTurn(backend, path, turn)
		readTime += time.Since(began)
		return value, err
	}
	// The selected file can be the currently active Codex session. Freeze its
	// current complete-record prefix; t.TempDir removes all content on exit.
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	snapshotPath := filepath.Join(t.TempDir(), "native.jsonl")
	snapshot, err := os.OpenFile(snapshotPath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if _, err = io.CopyN(snapshot, source, info.Size()); err != nil {
		t.Fatal(err)
	}
	// Provider writes can end halfway through a JSON record at the freeze point.
	end := info.Size()
	for end > 0 {
		start := max(int64(0), end-64*1024)
		block := make([]byte, end-start)
		if _, err = snapshot.ReadAt(block, start); err != nil {
			t.Fatal(err)
		}
		if last := bytes.LastIndexByte(block, '\n'); last >= 0 {
			end = start + int64(last) + 1
			break
		}
		end = start
	}
	if err = snapshot.Truncate(end); err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	path = snapshotPath
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	chat := chatmodel.Session{ID: "profile-session", Backend: chatmodel.BackendCodex, NativeSessionPath: path}
	report := map[string]any{"source_bytes": info.Size(), "frozen_complete_record_snapshot": true, "detail_page_limit_bytes": toolDetailPageBytes}
	measure := func(run func()) historyProfileSample {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		run()
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		return historyProfileSample{float64(elapsed.Microseconds()) / 1000, after.TotalAlloc - before.TotalAlloc}
	}
	var full chatmodel.Session
	report["cold_parse_and_cache"] = measure(func() { full, err = store.readHistory(chat) })
	report["native_index_ms"] = float64(indexTime.Microseconds()) / 1000
	report["native_turn_decode_ms"] = float64(readTime.Microseconds()) / 1000
	if err != nil {
		t.Fatal("native parse failed", err)
	}
	report["turns"], report["parts"] = len(full.Turns), len(full.Transcript.Parts)
	toolCount, toolBytes := 0, 0
	for _, part := range full.Transcript.Parts {
		if toolPart(part) {
			toolCount++
			toolBytes += len(part.Text)
		}
	}
	report["tool_parts"], report["tool_body_bytes"] = toolCount, toolBytes
	warm := []historyProfileSample{}
	for range 5 {
		warm = append(warm, measure(func() { _, err = store.readHistory(chat) }))
		if err != nil {
			t.Fatal(err)
		}
	}
	report["warm_cached_reads"] = warm
	store.historyCache.mu.Lock()
	report["cache_accounted_bytes"] = store.historyCache.bytes
	for _, file := range store.historyCache.files {
		report["separate_tool_bodies"] = len(file.details)
	}
	store.historyCache.mu.Unlock()
	for _, lazy := range []bool{false, true} {
		name := "full"
		if lazy {
			name = "lazy"
		}
		cursor, pages, bytes, maximum := "", 0, 0, 0
		samples := []historyProfileSample{}
		for {
			var page chatmodel.Session
			var writer historyProfileWriter
			samples = append(samples, measure(func() {
				// Match production requests: hit the native cache, rebuild the
				// session, select a page, project summaries, then encode JSON.
				var snapshot chatmodel.Session
				snapshot, err = store.readHistory(chat)
				if err != nil {
					return
				}
				page, err = historyPage(snapshot, cursor)
				if err != nil {
					return
				}
				if lazy {
					page = slimToolDetails(page)
				}
				err = json.NewEncoder(&writer).Encode(page)
			}))
			if err != nil {
				t.Fatal("profile page failed", err)
			}
			pages++
			bytes += writer.bytes
			maximum = max(maximum, writer.bytes)
			if !page.History.HasMore {
				break
			}
			if page.History.Before == cursor {
				t.Fatal("history cursor did not advance")
			}
			cursor = page.History.Before
		}
		report[name+"_history"] = map[string]any{"pages": pages, "json_bytes": bytes, "largest_page_bytes": maximum, "samples": samples}
	}
	slim := slimToolDetails(full)
	kinds := map[string]map[string]int{}
	for _, part := range slim.Transcript.Parts {
		if part.Tool == nil {
			continue
		}
		kind := part.Kind
		if part.Tool.Kind == "thinking" {
			kind = "thinking"
		}
		if kinds[kind] == nil {
			kinds[kind] = map[string]int{}
		}
		kinds[kind]["parts"]++
		kinds[kind]["bytes"] += part.Tool.Bytes
		kinds[kind]["largest_bytes"] = max(kinds[kind]["largest_bytes"], part.Tool.Bytes)
	}
	report["tool_kinds"] = kinds
	for kind, metrics := range kinds {
		ref := ""
		for _, part := range slim.Transcript.Parts {
			if part.Tool == nil {
				continue
			}
			partKind := part.Kind
			if part.Tool.Kind == "thinking" {
				partKind = "thinking"
			}
			if partKind == kind && part.Tool.Bytes == metrics["largest_bytes"] {
				ref = part.Tool.Ref
				break
			}
		}
		samples := []historyProfileSample{}
		for range 5 {
			samples = append(samples, measure(func() {
				snapshot, readErr := store.readHistory(chat)
				if readErr != nil {
					err = readErr
					return
				}
				page, ok := readToolDetailPage(snapshot, ref, 0, "")
				if !ok {
					err = fmt.Errorf("selected detail unavailable")
					return
				}
				err = json.NewEncoder(&historyProfileWriter{}).Encode(page)
			}))
		}
		if err != nil {
			t.Fatal(err)
		}
		report[kind+"_first_detail_page"] = samples
	}
	largest, ref := 0, ""
	for _, part := range slim.Transcript.Parts {
		if part.Tool != nil && part.Tool.Bytes > largest {
			largest, ref = part.Tool.Bytes, part.Tool.Ref
		}
	}
	if ref != "" {
		samples := []historyProfileSample{}
		for range 5 {
			samples = append(samples, measure(func() {
				snapshot, readErr := store.readHistory(chat)
				if readErr != nil {
					err = readErr
					return
				}
				page, ok := readToolDetailPage(snapshot, ref, 0, "")
				if !ok {
					err = fmt.Errorf("selected tool unavailable")
					return
				}
				writer := &historyProfileWriter{}
				err = json.NewEncoder(writer).Encode(page)
			}))
		}
		if err != nil {
			t.Fatal(err)
		}
		report["largest_tool_bytes"], report["first_detail_page"] = largest, samples
	}
	if final, statErr := os.Stat(path); statErr != nil || !sameHistoryFile(info, final) {
		t.Fatal("source changed during profiling; rerun on a stable transcript")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("HISTORY_PROFILE " + string(encoded))
}
