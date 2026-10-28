package model

import (
	"strings"
	"unicode/utf8"
)

// Part is one normalized piece of a session transcript.
type Part struct {
	Role     string `json:"role"`
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Text     string `json:"text"`
	Cut      int    `json:"cut,omitempty"`
	Model    string `json:"model,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// Transcript is a session's conversation in the order it was said.
type Transcript struct {
	Parts    []Part `json:"parts"`
	Cut      bool   `json:"cut,omitempty"`
	Source   string `json:"source,omitempty"`
	Captured int    `json:"captured,omitempty"`
}

const (
	transcriptPartMax = 8000
	transcriptMax     = 400_000
)

// AppendPart applies the same per-part and whole-transcript bounds as Magpie.
func AppendPart(t *Transcript, part Part) bool {
	part.Text = strings.TrimSpace(part.Text)
	if part.Text == "" {
		return true
	}
	if count := utf8.RuneCountInString(part.Text); count > transcriptPartMax {
		runes := []rune(part.Text)
		part.Text = string(runes[:transcriptPartMax])
		part.Cut += count - transcriptPartMax
	}
	size := 0
	for _, existing := range t.Parts {
		size += len(existing.Text)
	}
	if size+len(part.Text) > transcriptMax {
		t.Cut = true
		return false
	}
	t.Parts = append(t.Parts, part)
	return true
}

// AppendStreamText joins protocol deltas without trimming whitespace between them.
func AppendStreamText(t *Transcript, part Part) {
	if part.Text == "" {
		return
	}
	remainingBytes := transcriptMax - transcriptSize(t.Parts)
	if remainingBytes <= 0 {
		t.Cut = true
		return
	}
	if len(part.Text) > remainingBytes {
		originalRunes := utf8.RuneCountInString(part.Text)
		var clipped strings.Builder
		for _, r := range part.Text {
			runeBytes := utf8.RuneLen(r)
			if runeBytes > remainingBytes {
				break
			}
			clipped.WriteRune(r)
			remainingBytes -= runeBytes
		}
		part.Text = clipped.String()
		part.Cut += originalRunes - utf8.RuneCountInString(part.Text)
		t.Cut = true
	}
	if part.Text == "" {
		return
	}
	if len(t.Parts) > 0 {
		last := &t.Parts[len(t.Parts)-1]
		if last.Role == part.Role && last.Kind == part.Kind && last.Name == part.Name && last.Model == part.Model {
			remaining := transcriptPartMax - utf8.RuneCountInString(last.Text)
			if remaining <= 0 {
				last.Cut += part.Cut + utf8.RuneCountInString(part.Text)
				return
			}
			runes := []rune(part.Text)
			if len(runes) > remaining {
				last.Text += string(runes[:remaining])
				last.Cut += part.Cut + len(runes) - remaining
			} else {
				last.Text += part.Text
				last.Cut += part.Cut
			}
			return
		}
	}
	runes := []rune(part.Text)
	if len(runes) > transcriptPartMax {
		part.Text = string(runes[:transcriptPartMax])
		part.Cut += len(runes) - transcriptPartMax
	}
	t.Parts = append(t.Parts, part)
}

func transcriptSize(parts []Part) int {
	size := 0
	for _, part := range parts {
		size += len(part.Text)
	}
	return size
}
