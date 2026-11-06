package agent

import (
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestNativeUserInputTimesSurviveTranscriptProjection(t *testing.T) {
	for _, test := range []struct {
		backend chatmodel.Backend
		records string
	}{
		{chatmodel.BackendCodex, `{"timestamp":"2026-11-06T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"First input"}]}}
{"timestamp":"2026-11-06T12:00:05.123Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Follow up"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"No timestamp"}]}}`},
		{chatmodel.BackendClaude, `{"timestamp":"2026-11-06T12:00:00Z","type":"user","message":{"role":"user","content":"First input"}}
{"timestamp":"2026-11-06T12:00:05.123Z","type":"user","message":{"role":"user","content":[{"type":"text","text":"Follow up"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}}
{"type":"user","message":{"role":"user","content":"No timestamp"}}`},
	} {
		t.Run(string(test.backend), func(t *testing.T) {
			transcript, err := parseTranscriptReader(test.backend, strings.NewReader(test.records), "", false)
			if err != nil {
				t.Fatal(err)
			}
			first, _ := time.Parse(time.RFC3339Nano, "2026-11-06T12:00:00Z")
			follow, _ := time.Parse(time.RFC3339Nano, "2026-11-06T12:00:05.123Z")
			if len(transcript.Parts) != 4 {
				t.Fatalf("Unexpected input projection: %+v", transcript.Parts)
			}
			for i, want := range []int64{first.UnixMilli(), follow.UnixMilli(), follow.UnixMilli(), 0} {
				if transcript.Parts[i].Timestamp != want {
					t.Fatalf("part %d timestamp: got %d, want %d", i, transcript.Parts[i].Timestamp, want)
				}
			}
		})
	}
}
