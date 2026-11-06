package workspace

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var imageToolName = regexp.MustCompile(`(?:^|\.|__)(?:view_?image|image_?view)$`)

// Preserve the complete native path separately from the shortened activity
// label. Lazy tool bodies are omitted from history responses.
func viewedImagePaths(name, text string) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if !imageToolName.MatchString(name) && name != "multi_tool_use.parallel" && name != "read" {
		return nil
	}
	var data map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &data) != nil {
		return nil
	}
	return imageCallPaths(name, data)
}

func imageCallPaths(name string, data map[string]json.RawMessage) []string {
	if imageToolName.MatchString(name) || name == "read" {
		for _, key := range []string{"path", "file_path", "image_path"} {
			var path string
			if json.Unmarshal(data[key], &path) == nil && strings.TrimSpace(path) != "" && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n") {
				if name == "read" {
					switch strings.ToLower(filepath.Ext(path)) {
					case ".png", ".jpg", ".jpeg", ".gif", ".webp":
					default:
						return nil
					}
				}
				return []string{path}
			}
		}
		return nil
	}
	if name != "multi_tool_use.parallel" {
		return nil
	}
	var calls []struct {
		Name       string                     `json:"recipient_name"`
		Parameters map[string]json.RawMessage `json:"parameters"`
	}
	if json.Unmarshal(data["tool_uses"], &calls) != nil || len(calls) == 0 || len(calls) > 64 {
		return nil
	}
	var paths []string
	for _, call := range calls {
		name := strings.ToLower(strings.TrimSpace(call.Name))
		// Mixed batches keep their ordinary tool details, including non-image work.
		if !imageToolName.MatchString(name) {
			return nil
		}
		images := imageCallPaths(name, call.Parameters)
		if len(images) == 0 {
			return nil
		}
		paths = append(paths, images...)
	}
	return paths
}
