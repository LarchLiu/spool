package model

// Part is one normalized piece of a session transcript.
type Part struct {
	ID       string `json:"id,omitempty"`
	Phase    string `json:"phase,omitempty"`
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

// AppendPart retains a complete normalized part for live and native history.
func AppendPart(t *Transcript, part Part) {
	if part.Text == "" && part.ImageURL == "" {
		return
	}
	t.Parts = append(t.Parts, part)
}

// AppendStreamText joins protocol deltas without trimming whitespace between them.
func AppendStreamText(t *Transcript, part Part) {
	if part.Text == "" {
		return
	}
	if len(t.Parts) > 0 {
		last := &t.Parts[len(t.Parts)-1]
		if last.ID == part.ID && last.Phase == part.Phase && last.Role == part.Role && last.Kind == part.Kind && last.Name == part.Name && last.Model == part.Model {
			last.Text += part.Text
			last.Cut += part.Cut
			return
		}
	}
	t.Parts = append(t.Parts, part)
}
