package agent

// Compaction is transient run state, never a transcript part or tool activity.
// Normal output also ends it in case an older CLI omits the end notification.
func compactionEvents(next func(AgentEvent) error) func(AgentEvent) error {
	active := false
	return func(event AgentEvent) error {
		if event.Type == "compaction" {
			if active == event.Compacting {
				return nil
			}
			active = event.Compacting
		} else if active && (event.Type == "delta" || event.Type == "part" || event.Type == "activity") {
			active = false
			if err := next(AgentEvent{Type: "compaction"}); err != nil {
				return err
			}
		}
		return next(event)
	}
}

func codexCompactionItem(item map[string]any) bool {
	switch codexItemKind(item) {
	case "contextcompaction", "compaction", "compacted":
		return true
	}
	return false
}

func claudeCompactionRecord(record map[string]any) bool {
	return record["isCompactSummary"] == true || record["type"] == "system" && record["subtype"] == "compact_boundary"
}
