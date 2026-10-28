package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranscriptJSONMatchesMagpieShape(t *testing.T) {
	transcript := Transcript{Parts: []Part{{Role: "assistant", Kind: "tool_use", Name: "shell", Text: "go test", Model: "gpt-6"}}}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"parts":[{"role":"assistant","kind":"tool_use","name":"shell","text":"go test","model":"gpt-6"}]}`
	if string(encoded) != want {
		t.Fatalf("transcript JSON = %s, want %s", encoded, want)
	}
}

func TestAppendPartUsesMagpieTranscriptBounds(t *testing.T) {
	transcript := Transcript{Parts: []Part{}}
	long := strings.Repeat("x", transcriptPartMax+3)
	if !AppendPart(&transcript, Part{Role: "user", Kind: "text", Text: long}) {
		t.Fatal("first part unexpectedly exhausted transcript")
	}
	if len(transcript.Parts) != 1 || len([]rune(transcript.Parts[0].Text)) != transcriptPartMax || transcript.Parts[0].Cut != 3 {
		t.Fatalf("unexpected clipped part: %#v", transcript.Parts)
	}
	transcript.Parts = []Part{{Role: "user", Kind: "text", Text: strings.Repeat("x", transcriptMax)}}
	if AppendPart(&transcript, Part{Role: "assistant", Kind: "text", Text: "overflow"}) || !transcript.Cut || len(transcript.Parts) != 1 {
		t.Fatalf("transcript limit was not reported: %#v", transcript)
	}
}

func TestAppendStreamTextUsesMagpiePartAndTranscriptBounds(t *testing.T) {
	stream := Transcript{Parts: []Part{{
		Role: "assistant", Kind: "text", Text: strings.Repeat("x", transcriptPartMax),
	}}}
	AppendStreamText(&stream, Part{Role: "assistant", Kind: "text", Text: "ab"})
	AppendStreamText(&stream, Part{Role: "assistant", Kind: "text", Text: "c"})
	if len(stream.Parts) != 1 || stream.Parts[0].Cut != 3 {
		t.Fatalf("same streamed part was not clipped consistently: %#v", stream.Parts)
	}

	full := Transcript{Parts: []Part{}}
	remaining := transcriptMax - 1
	for remaining > 0 {
		size := min(transcriptPartMax, remaining)
		full.Parts = append(full.Parts, Part{Role: "assistant", Kind: "text", Text: strings.Repeat("x", size)})
		remaining -= size
	}
	AppendStreamText(&full, Part{Role: "assistant", Kind: "text", Text: "yz"})
	if size := transcriptSize(full.Parts); size != transcriptMax || !full.Cut || !strings.HasSuffix(full.Parts[len(full.Parts)-1].Text, "y") {
		t.Fatalf("stream exceeded Magpie transcript bound: bytes=%d cut=%v last=%#v", size, full.Cut, full.Parts[len(full.Parts)-1])
	}
}

func TestAppendPartRejectsMultibyteTextCrossingBudget(t *testing.T) {
	transcript := Transcript{Parts: []Part{{Role: "assistant", Kind: "text", Text: strings.Repeat("x", transcriptMax-2)}}}
	if AppendPart(&transcript, Part{Role: "assistant", Kind: "text", Text: "\u4e2d"}) || !transcript.Cut || len(transcript.Parts) != 1 {
		t.Fatal("a multibyte part crossed the page budget")
	}
}
