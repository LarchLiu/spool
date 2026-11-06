package model

// Part is one normalized piece of a session transcript.
type Part struct {
	ID       string       `json:"id,omitempty"`
	Phase    string       `json:"phase,omitempty"`
	Role     string       `json:"role"`
	Kind     string       `json:"kind"`
	Name     string       `json:"name,omitempty"`
	Text     string       `json:"text"`
	Cut      int          `json:"cut,omitempty"`
	Model    string       `json:"model,omitempty"`
	ImageURL string       `json:"image_url,omitempty"`
	FilePath string       `json:"file_path,omitempty"`
	Images   []string     `json:"images,omitempty"`
	Tool     *ToolSummary `json:"tool,omitempty"`
}

// ToolSummary is enough to render a collapsed activity without its input/output.
// Ref identifies the part within an authorized chat; Version identifies its body.
type ToolSummary struct {
	Ref        string       `json:"ref,omitempty"`
	Href       string       `json:"href,omitempty"`
	Version    string       `json:"version"`
	Bytes      int          `json:"bytes"`
	Kind       string       `json:"kind"`
	Path       string       `json:"path,omitempty"`
	ImagePaths []string     `json:"image_paths,omitempty"`
	ImageCount int          `json:"image_count,omitempty"`
	ImageURLs  []string     `json:"image_urls,omitempty"`
	Query      string       `json:"query,omitempty"`
	Preview    string       `json:"preview,omitempty"`
	Stats      string       `json:"stats,omitempty"`
	Changes    *ToolChanges `json:"changes,omitempty"`
	// Shared immutable source used to detect edits to hydrated cache responses.
	SourceText string `json:"-"`
}

type ToolChanges struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
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
