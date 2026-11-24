package agent

import (
	"regexp"
	"strings"

	chatmodel "spool/internal/model"
)

var claudeCommandFrames = regexp.MustCompile(`(?s)<local-command-stdout>(.*?)</local-command-stdout>|<local-command-stderr>(.*?)</local-command-stderr>`)
var claudeCommandEntities = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">")

// Claude persists command replies as meta system records, but streams them as
// synthetic assistant messages. Both represent a visible answer, not carry notes.
func claudeCommandOutput(record map[string]any) (chatmodel.Part, bool) {
	if record["isSidechain"] == true || claudeCompactionRecord(record) {
		return chatmodel.Part{}, false
	}
	message, _ := record["message"].(map[string]any)
	var text string
	switch {
	case record["type"] == "system" && record["subtype"] == "local_command":
		text = claudeCommandText(stringValue(record["content"]))
	case record["type"] == "assistant" && stringValue(record["local_command_source"]) != "":
		// The CLI already decodes assistant content. Its source can be a command
		// name in older versions, so it must not replace the actual reply.
		for _, part := range claudeTranscriptParts("assistant", message["content"], "") {
			if part.Kind == "text" {
				text += part.Text
			}
		}
	default:
		return chatmodel.Part{}, false
	}
	id := stringValue(record["uuid"])
	if id == "" {
		id = stringValue(message["id"])
	}
	return chatmodel.Part{ID: id, Role: "assistant", Kind: "text", Phase: "final_answer", Text: strings.TrimSpace(text)}, true
}

func claudeCommandText(source string) string {
	if frames := claudeCommandFrames.FindAllStringSubmatch(source, -1); len(frames) > 0 {
		texts := make([]string, 0, len(frames))
		for _, frame := range frames {
			texts = append(texts, frame[1]+frame[2])
		}
		source = strings.Join(texts, "\n")
	}
	// Match the CLI's single decoding pass; &amp;lt; remains &lt;.
	return claudeCommandEntities.Replace(source)
}
