package agent

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	chatmodel "spool/internal/model"
)

// Converted records keep their display origin separate from the native format
// used to resume them. This metadata contains no transcript or instructions.
type sessionOrigin struct {
	Backend         chatmodel.Backend `json:"backend"`
	Model           string            `json:"model,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
}

func recordOrigin(record map[string]any) sessionOrigin {
	value, _ := record["spool_origin"].(map[string]any)
	origin := sessionOrigin{Backend: chatmodel.Backend(stringValue(value["backend"])), Model: stringValue(value["model"]), ReasoningEffort: stringValue(value["reasoning_effort"])}
	if !origin.Backend.Valid() {
		return sessionOrigin{}
	}
	return origin
}

func claudeRecordOrigin(record map[string]any) sessionOrigin {
	if origin := recordOrigin(record); origin.Backend.Valid() {
		return origin
	}
	message, _ := record["message"].(map[string]any)
	// Before origins were recorded, our converter wrote this exact synthetic
	// envelope. Native synthetic CLI responses alone must not imply Codex.
	if legacyClaudeCarry(record, message) {
		return sessionOrigin{Backend: chatmodel.BackendCodex}
	}
	model := stringValue(message["model"])
	if model == "<synthetic>" {
		model = ""
	}
	return sessionOrigin{Backend: chatmodel.BackendClaude, Model: model, ReasoningEffort: stringValue(record["perTurnEffort"])}
}

func legacyClaudeCarry(record, message map[string]any) bool {
	if record["version"] != "2.0.0" || record["entrypoint"] != "cli" || record["userType"] != "external" || record["isSidechain"] != false {
		return false
	}
	if _, err := uuid.Parse(stringValue(record["uuid"])); err != nil {
		return false
	}
	if _, err := uuid.Parse(stringValue(record["sessionId"])); err != nil {
		return false
	}
	if record["type"] == "user" && message["role"] == "user" {
		// A converted turn can contain only user input (for example after
		// compaction). Its generated envelope still identifies its source;
		// there need not be a later assistant message to supply that origin.
		for _, key := range []string{"promptId", "permissionMode", "promptSource", "turnOrigin", "turnPosition", "isMeta"} {
			if _, exists := record[key]; exists {
				return false
			}
		}
		return true
	}
	if record["type"] != "assistant" || message["model"] != "<synthetic>" {
		return false
	}
	id, ok := strings.CutPrefix(stringValue(message["id"]), "msg_")
	if !ok || len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// User records precede the model metadata on native assistant/context records.
// Fill the whole turn only after reading it, including its original prompt.
func completeCarryOrigins(items []carryItem, backend chatmodel.Backend) {
	start := 0
	origin := sessionOrigin{Backend: backend}
	flush := func(end int) {
		for i := start; i < end; i++ {
			items[i].Origin = origin
		}
	}
	for i := range items {
		it := &items[i]
		if it.Kind == "user" {
			flush(i)
			start, origin = i, sessionOrigin{Backend: backend}
		}
		if it.Kind == "result" || !it.Origin.Backend.Valid() {
			continue
		}
		if it.Origin.Backend != origin.Backend {
			origin = it.Origin
		} else {
			if it.Origin.Model != "" {
				origin.Model = it.Origin.Model
			}
			if it.Origin.ReasoningEffort != "" {
				origin.ReasoningEffort = it.Origin.ReasoningEffort
			}
		}
	}
	flush(len(items))
}

func decodeOrigin(raw json.RawMessage) sessionOrigin {
	var origin sessionOrigin
	if json.Unmarshal(raw, &origin) != nil || !origin.Backend.Valid() {
		return sessionOrigin{}
	}
	return origin
}
