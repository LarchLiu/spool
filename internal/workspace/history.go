package workspace

import (
	"strings"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// historicalSnapshot enriches the SQLite transcript with provider-owned
// history. SQLite intentionally stores the normalized turn state, while the
// native JSONL remains the source of truth for command output, diffs, and
// readable reasoning summaries.
func (a *App) historicalSnapshot(chat chatmodel.Session) chatmodel.Session {
	if chat.Transcript.Source != "" {
		return chat
	}
	if chat.NativeSessionID == "" || chat.Status == chatmodel.StatusRunning || len(chat.Turns) == 0 {
		return chat
	}
	workingDirectory := ""
	if chat.ProjectID != "" {
		if project, err := a.store.Project(chat.ProjectID); err == nil {
			workingDirectory = project.Path
		}
	}
	path, err := agent.FindSessionTranscript(chat.Backend, chat.NativeSessionID, workingDirectory)
	if err != nil {
		return chat
	}
	native, err := agent.ParseSessionTranscript(chat.Backend, path)
	if err != nil || len(native.Parts) == 0 {
		return chat
	}
	merged, ok := mergeHistoricalTurns(chat, native)
	if !ok {
		return chat
	}
	// Do not expose local filesystem paths to a web client.
	merged.Transcript.Source = string(chat.Backend) + "-native"
	merged.Transcript.Captured = len(native.Parts)
	return merged
}

func (s *Store) hydrateSession(chat chatmodel.Session) chatmodel.Session {
	if chat.NativeSessionID == "" || chat.Status == chatmodel.StatusRunning || len(chat.Turns) == 0 {
		return chat
	}
	path := chat.NativeSessionPath
	if path == "" {
		workingDirectory := ""
		if chat.ProjectID != "" {
			if project, err := s.Project(chat.ProjectID); err == nil {
				workingDirectory = project.Path
			}
		}
		path, _ = agent.FindSessionTranscript(chat.Backend, chat.NativeSessionID, workingDirectory)
	}
	if path == "" {
		return chat
	}
	native, err := agent.ParseSessionTranscript(chat.Backend, path)
	if err != nil || len(native.Parts) == 0 {
		return chat
	}
	merged, ok := mergeHistoricalTurns(chat, native)
	if !ok {
		return chat
	}
	merged.Transcript.Source = string(chat.Backend) + "-native"
	merged.Transcript.Captured = len(native.Parts)
	return merged
}

func mergeHistoricalTurns(session chatmodel.Session, native chatmodel.Transcript) (chatmodel.Session, bool) {
	if len(session.Turns) == 0 || len(native.Parts) == 0 {
		return session, false
	}
	if len(session.Transcript.Parts) == 0 {
		return rebuildHistoricalTurns(session, native)
	}
	markers := nativeUserMarkers(native.Parts)
	if len(markers) > len(session.Turns) {
		markers = markers[len(markers)-len(session.Turns):]
	}
	local := session.Transcript.Parts
	merged := make([]chatmodel.Part, 0, len(local)+len(native.Parts))
	turns := make([]chatmodel.Turn, 0, len(session.Turns))
	nativeCursor := 0
	matched := 0
	for turnIndex, sourceTurn := range session.Turns {
		start := clamp(sourceTurn.PartStart, 0, len(local))
		end := clamp(sourceTurn.PartEnd, start, len(local))
		inputEnd := clamp(sourceTurn.InputEnd, start, end)
		input := append([]chatmodel.Part(nil), local[start:inputEnd]...)
		localOutput := append([]chatmodel.Part(nil), local[inputEnd:end]...)
		marker := -1
		if turnIndex < len(markers) {
			marker = markers[turnIndex]
			if marker < nativeCursor {
				marker = -1
			}
		}
		// A caller may still provide a live prompt (for example, while a
		// completed turn is being reconciled). Prefer its exact text when it
		// is available, but never require prompt text to be persisted in SQLite.
		if sourceTurn.Prompt != "" {
			if exact := findNativePrompt(native.Parts, nativeCursor, sourceTurn.Prompt); exact >= 0 {
				marker = exact
			}
		}
		output := localOutput
		if marker >= 0 {
			matched++
			nativeCursor = marker + 1
			end := len(native.Parts)
			for _, nextMarker := range markers {
				if nextMarker > marker {
					end = nextMarker
					break
				}
			}
			output = nativeAssistantPartsUntil(native.Parts, nativeCursor, end)
			nativeCursor = end
			if len(output) == 0 {
				output = localOutput
			}
		}
		turn := sourceTurn
		turn.PartStart = len(merged)
		merged = append(merged, input...)
		turn.InputEnd = len(merged)
		merged = append(merged, output...)
		turn.PartEnd = len(merged)
		turn.Parts = append([]chatmodel.Part(nil), merged[turn.PartStart:turn.PartEnd]...)
		populateTurnContent(&turn)
		turns = append(turns, turn)
	}
	if matched == 0 {
		return session, false
	}
	session.Transcript.Parts = merged
	session.Turns = turns
	return session, true
}

func rebuildHistoricalTurns(session chatmodel.Session, native chatmodel.Transcript) (chatmodel.Session, bool) {
	markers := nativeUserMarkers(native.Parts)
	if len(markers) < len(session.Turns) {
		// A provider can omit the user message from a paginated/truncated
		// rollout. A single-turn chat can still display the assistant/tool
		// records instead of appearing empty.
		if len(session.Turns) != 1 || len(markers) != 0 {
			return session, false
		}
		markers = []int{0}
	}
	if len(markers) > len(session.Turns) {
		// Native Codex sessions may contain an initial environment/context
		// message in addition to user prompts. Keep the most recent turn
		// markers when the provider has more boundaries than Spool knows about.
		markers = markers[len(markers)-len(session.Turns):]
	}
	if len(markers) < len(session.Turns) {
		return session, false
	}
	merged := make([]chatmodel.Part, 0, len(native.Parts))
	turns := make([]chatmodel.Turn, 0, len(session.Turns))
	for index, sourceTurn := range session.Turns {
		start := markers[index]
		end := len(native.Parts)
		if index+1 < len(markers) {
			end = markers[index+1]
		}
		turn := sourceTurn
		turn.PartStart = len(merged)
		for _, part := range native.Parts[start:end] {
			if part.Role == "user" {
				if part.Kind == "text" || part.Kind == "image" {
					merged = append(merged, part)
				}
				continue
			}
			if part.Role == "assistant" || part.Role == "tool" {
				merged = append(merged, part)
			}
		}
		turn.InputEnd = turn.PartStart
		for turn.InputEnd < len(merged) && merged[turn.InputEnd].Role == "user" {
			turn.InputEnd++
		}
		turn.PartEnd = len(merged)
		turn.Parts = append([]chatmodel.Part(nil), merged[turn.PartStart:turn.PartEnd]...)
		populateTurnContent(&turn)
		turns = append(turns, turn)
	}
	session.Transcript.Parts = merged
	session.Turns = turns
	return session, true
}

func findNativePrompt(parts []chatmodel.Part, from int, prompt string) int {
	from = clamp(from, 0, len(parts))
	want := strings.TrimSpace(prompt)
	for i := from; i < len(parts); i++ {
		part := parts[i]
		if part.Role != "user" || part.Kind != "text" {
			continue
		}
		if want == "" || strings.TrimSpace(part.Text) == want || strings.Contains(part.Text, want) {
			return i
		}
	}
	return -1
}

// nativeUserMarkers returns one boundary per native user message. A single
// message can contain several adjacent input blocks (text plus image/context),
// so only its first user text/image block is a boundary. Context-only blocks
// are intentionally ignored; Codex writes those as synthetic user records
// before the actual prompt.
func nativeUserMarkers(parts []chatmodel.Part) []int {
	markers := make([]int, 0)
	inUserMessage := false
	marked := false
	for index, part := range parts {
		if part.Role == "user" {
			if !inUserMessage {
				inUserMessage = true
				marked = false
			}
			if !marked && (part.Kind == "text" || part.Kind == "image") && !syntheticUserPart(part) {
				markers = append(markers, index)
				marked = true
			}
			continue
		}
		inUserMessage = false
		marked = false
	}
	return markers
}

func syntheticUserPart(part chatmodel.Part) bool {
	if part.Kind == "context" {
		return true
	}
	text := strings.TrimSpace(part.Text)
	for _, prefix := range []string{
		"<environment_context>",
		"<app-context>",
		"<skills_instructions>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<tools>",
		"<multi_agent_mode>",
		"<codex_apps_client_time_context>",
		"<image_resize_notice>",
		"# AGENTS.md",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func nativeAssistantPartsUntil(parts []chatmodel.Part, from, end int) []chatmodel.Part {
	from = clamp(from, 0, len(parts))
	end = clamp(end, from, len(parts))
	output := make([]chatmodel.Part, 0)
	for i := from; i < end; i++ {
		part := parts[i]
		if part.Role == "assistant" || part.Role == "tool" {
			output = append(output, part)
		}
	}
	return output
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
