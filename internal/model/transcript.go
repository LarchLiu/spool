package model

// Part is one normalized piece of a session transcript.
type Part struct {
	Replace   bool         `json:"-"` // a complete live tool body, rather than a delta
	ID        string       `json:"id,omitempty"`
	Phase     string       `json:"phase,omitempty"`
	Timestamp int64        `json:"timestamp,omitempty"`
	Role      string       `json:"role"`
	Kind      string       `json:"kind"`
	Name      string       `json:"name,omitempty"`
	Text      string       `json:"text"`
	Cut       int          `json:"cut,omitempty"`
	Model     string       `json:"model,omitempty"`
	ImageURL  string       `json:"image_url,omitempty"`
	FilePath  string       `json:"file_path,omitempty"`
	Images    []string     `json:"images,omitempty"`
	Tool      *ToolSummary `json:"tool,omitempty"`
	// Native is what the provider itself said about a tool call. It never
	// leaves the server: clients receive it through Tool.
	Native *ToolNative `json:"-"`
}

// Tool statuses, as either provider reports them.
const (
	ToolRunning   = "running"
	ToolCompleted = "completed"
	ToolFailed    = "failed"
	ToolDeclined  = "declined"
)

// ToolNative carries provider-structured facts beside a call's display text,
// so a summary does not have to guess them back from that text.
type ToolNative struct {
	// Kind is a display kind the provider implies (for example "web").
	Kind       string
	Path       string
	Query      string
	Status     string
	ExitCode   *int
	DurationMs int64
	// Operations are the provider's own reading of one shell command.
	Operations []ToolOperation
	Files      []ToolFile
}

// ToolSummary is enough to render a collapsed activity without its input/output.
// Ref identifies the part within an authorized chat; Version identifies its body.
type ToolSummary struct {
	Ref        string          `json:"ref,omitempty"`
	Href       string          `json:"href,omitempty"`
	Version    string          `json:"version"`
	Bytes      int             `json:"bytes"`
	Kind       string          `json:"kind"`
	Path       string          `json:"path,omitempty"`
	ImagePaths []string        `json:"image_paths,omitempty"`
	ImageCount int             `json:"image_count,omitempty"`
	ImageURLs  []string        `json:"image_urls,omitempty"`
	Query      string          `json:"query,omitempty"`
	Preview    string          `json:"preview,omitempty"`
	Stats      string          `json:"stats,omitempty"`
	Changes    *ToolChanges    `json:"changes,omitempty"`
	Operations []ToolOperation `json:"operations,omitempty"`
	// Status is absent when the provider did not report one. A call's status
	// may arrive on its use or on its result.
	Status     string     `json:"status,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
	Files      []ToolFile `json:"files,omitempty"`
	// Shared immutable native facts used with SourceText to detect stale summaries.
	SourceNative *ToolNative `json:"-"`
	// Shared immutable source used to detect edits to hydrated cache responses.
	SourceText string `json:"-"`
}

// ToolOperation is a display-only input summary, without invented output slices.
type ToolOperation struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Path    string `json:"path,omitempty"`
	Query   string `json:"query,omitempty"`
}

// ToolFile is one file of a native file change.
type ToolFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"` // create | write | delete
	MovePath  string `json:"move_path,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
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
// A named tool call is kept even without input: its result still needs it.
func AppendPart(t *Transcript, part Part) {
	if part.Text == "" && part.ImageURL == "" && !(part.Kind == "tool_use" && part.Name != "" && part.ID != "") && !(part.Kind == "tool_result" && part.ID != "" && part.Native != nil) {
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
