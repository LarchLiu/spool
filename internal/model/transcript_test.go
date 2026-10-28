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

func TestAppendPartPreservesCompleteContent(t *testing.T) {
	transcript := Transcript{Parts: []Part{}}
	long := " \n" + strings.Repeat("\u4e2d", 300_000) + "\n "
	for range 3 {
		AppendPart(&transcript, Part{Role: "assistant", Kind: "text", Text: long})
	}
	if len(transcript.Parts) != 3 || transcript.Cut {
		t.Fatal("complete parts were dropped or marked as truncated")
	}
	for _, part := range transcript.Parts {
		if part.Text != long || part.Cut != 0 {
			t.Fatal("part content or whitespace was truncated")
		}
	}
}

func TestAppendStreamTextPreservesAllDeltas(t *testing.T) {
	stream := Transcript{Parts: []Part{}}
	delta := strings.Repeat("x", 10_000) + " \n"
	for range 100 {
		AppendStreamText(&stream, Part{Role: "assistant", Kind: "text", Text: delta})
	}
	if len(stream.Parts) != 1 || stream.Parts[0].Text != strings.Repeat(delta, 100) || stream.Parts[0].Cut != 0 || stream.Cut {
		t.Fatal("streamed content was dropped or truncated")
	}
}

func TestAppendPartRetainsImagesAndSkipsOnlyEmptyParts(t *testing.T) {
	transcript := Transcript{}
	AppendPart(&transcript, Part{})
	AppendPart(&transcript, Part{Role: "user", Kind: "image", ImageURL: "data:image/png;base64,eA=="})
	AppendPart(&transcript, Part{Role: "assistant", Kind: "text", Text: " \n"})
	if len(transcript.Parts) != 2 || transcript.Parts[0].ImageURL == "" || transcript.Parts[1].Text != " \n" {
		t.Fatal("image or whitespace-only content was dropped")
	}
}
