package agent

import (
	"context"
	"fmt"
	"time"

	chatmodel "spool/internal/model"
)

func DiscoverClaudeModels(ctx context.Context, binary string) (chatmodel.CodexCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	dir, err := WorkingDirectory("")
	if err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	p, err := startAgentProcess(ctx, AgentRequest{Backend: chatmodel.BackendClaude, Binary: binary, WorkingDirectory: dir})
	if err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	defer func() { cancel(); <-p.done }()
	if err = p.write(map[string]any{"type": "control_request", "request_id": "spool-model-discovery", "request": map[string]any{"subtype": "initialize", "hooks": nil}}); err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	for {
		v, e := p.read(ctx)
		if e != nil {
			return chatmodel.CodexCatalog{}, e
		}
		if v["type"] == "control_request" {
			if e = p.denyClaudeRequest(v); e != nil {
				return chatmodel.CodexCatalog{}, e
			}
			continue
		}
		if v["type"] != "control_response" || nestedString(v, "response", "request_id") != "spool-model-discovery" {
			continue
		}
		if nestedString(v, "response", "subtype") != "success" {
			return chatmodel.CodexCatalog{}, fmt.Errorf("Claude model discovery failed")
		}
		response, _ := v["response"].(map[string]any)
		body, _ := response["response"].(map[string]any)
		models, _ := body["models"].([]any)
		catalog := chatmodel.CodexCatalog{Models: []chatmodel.CodexModel{}}
		for _, item := range models {
			model, _ := item.(map[string]any)
			id := stringValue(model["value"])
			if id == "" {
				id = stringValue(model["model"])
			}
			if id == "" || chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: id}) != nil {
				continue
			}
			name := stringValue(model["displayName"])
			if name == "" {
				name = id
			}
			entry := chatmodel.CodexModel{Model: id, DisplayName: name, Efforts: []chatmodel.ReasoningOption{}}
			if levels, ok := model["supportedEffortLevels"].([]any); ok && model["supportsEffort"] != false {
				seen := make(map[string]bool)
				for _, level := range levels {
					if value := stringValue(level); value != "" && chatmodel.ValidateClaudeEffort(value) == nil && !seen[value] {
						seen[value] = true
						entry.Efforts = append(entry.Efforts, chatmodel.ReasoningOption{Effort: value})
					}
				}
			}
			catalog.Models = append(catalog.Models, entry)
		}
		if len(catalog.Models) == 0 {
			return catalog, fmt.Errorf("installed Claude CLI did not advertise models during initialization")
		}
		return catalog, nil
	}
}
