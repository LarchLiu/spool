package model

type Backend string

const (
	BackendCodex  Backend = "codex"
	BackendClaude Backend = "claude"
)

func (b Backend) Valid() bool { return b == BackendCodex || b == BackendClaude }

func (b Backend) Label() string {
	if b == BackendClaude {
		return "Claude Code"
	}
	return "Codex"
}

type SessionStatus string

const (
	StatusReady       SessionStatus = "ready"
	StatusRunning     SessionStatus = "running"
	StatusCompleted   SessionStatus = "completed"
	StatusFailed      SessionStatus = "failed"
	StatusStopped     SessionStatus = "stopped"
	StatusInterrupted SessionStatus = "interrupted"
)

type Turn struct {
	ID              string           `json:"id"`
	Backend         Backend          `json:"backend,omitempty"`
	PartStart       int              `json:"part_start"`
	InputEnd        int              `json:"input_end"`
	PartEnd         int              `json:"part_end"`
	StartedAt       int64            `json:"started_at"`
	FinishedAt      int64            `json:"finished_at,omitempty"`
	Status          SessionStatus    `json:"status"`
	Model           string           `json:"model,omitempty"`
	RequestedModel  string           `json:"requested_model,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	Error           string           `json:"error,omitempty"`
	PermissionMode  string           `json:"permission_mode,omitempty"`
	Attachments     []Attachment     `json:"attachments,omitempty"`
	PendingApproval *ApprovalRequest `json:"pending_approval,omitempty"`
	Prompt          string           `json:"-"`
	Reply           string           `json:"-"`
	Activities      []string         `json:"-"`
	Parts           []Part           `json:"-"`
}

type Session struct {
	AutomationChat    bool          `json:"automation_chat,omitempty"`
	CollaboratorIDs   []string      `json:"collaborator_ids,omitempty"`
	Artifacts         []Artifact    `json:"artifacts,omitempty"`
	UserID            string        `json:"user_id"`
	ProjectID         string        `json:"project_id"`
	ID                string        `json:"id"`
	Title             string        `json:"title"`
	Backend           Backend       `json:"backend"`
	NativeSessionID   string        `json:"native_session_id,omitempty"`
	NativeSessionPath string        `json:"-"`
	Model             string        `json:"model,omitempty"`
	ReasoningEffort   string        `json:"reasoning_effort,omitempty"`
	PermissionMode    string        `json:"permission_mode,omitempty"`
	CreatedAt         int64         `json:"created_at"`
	UpdatedAt         int64         `json:"updated_at"`
	Status            SessionStatus `json:"status"`
	Transcript        Transcript    `json:"transcript"`
	Turns             []Turn        `json:"turns"`
}

type Artifact struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}
