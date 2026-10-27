package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

func effortLabel(effort string) string {
	if effort == "" {
		return "Default"
	}
	if effort == "xhigh" {
		return "Extra High"
	}
	return strings.ToUpper(effort[:1]) + effort[1:]
}

func fallbackReasoningOptions() []chatmodel.ReasoningOption {
	var options []chatmodel.ReasoningOption
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		options = append(options, chatmodel.ReasoningOption{Effort: effort})
	}
	return options
}

func (p *agentProcess) initializeCodex(ctx context.Context) error {
	if p.initialized {
		return nil
	}
	if _, err := p.codexRPC(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "spool", "title": "Spool", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return err
	}
	if err := p.write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return err
	}
	p.initialized = true
	return nil
}

func (r *CLIRunner) CodexModels(ctx context.Context) (chatmodel.CodexCatalog, error) {
	binary, err := r.Binary(chatmodel.BackendCodex)
	if err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	directory, err := WorkingDirectory("")
	if err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	r.catalogMu.Lock()
	defer r.catalogMu.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return chatmodel.CodexCatalog{}, fmt.Errorf("agent runner is closed")
	}
	r.mu.Unlock()
	key := binary + "\n" + directory
	if r.catalogKey == key && time.Now().Before(r.catalogUntil) {
		return r.catalog.Clone(), r.catalogErr
	}
	// Discovery never creates a thread or sends a model request. Its short
	// deadline is separate from the unlimited lifetime of an active chat turn.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return chatmodel.CodexCatalog{}, fmt.Errorf("agent runner is closed")
	}
	r.catalogCancel = cancel
	r.runs.Add(1)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.catalogCancel = nil
		r.mu.Unlock()
		r.runs.Done()
	}()
	// Briefly cache failures too, so a disconnected CLI does not stall every
	// page request with a fresh discovery attempt.
	r.catalogKey, r.catalogUntil = key, time.Now().Add(15*time.Second)
	r.catalogErr = fmt.Errorf("Codex model discovery failed; retry shortly")
	process, err := startAgentProcess(ctx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, WorkingDirectory: directory})
	if err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	defer func() { cancel(); <-process.done }()
	if err := process.initializeCodex(ctx); err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	config, err := process.codexRPC(ctx, "config/read", map[string]any{"includeLayers": false, "cwd": directory})
	if err != nil {
		return chatmodel.CodexCatalog{}, err
	}
	catalog := chatmodel.CodexCatalog{
		DefaultModel: nestedString(config, "config", "model"), DefaultEffort: nestedString(config, "config", "model_reasoning_effort"),
		Models: []chatmodel.CodexModel{},
	}
	cursor := ""
	seen := make(map[string]bool)
	for page := 0; page < 20; page++ {
		params := map[string]any{"limit": 100, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, err := process.codexRPC(ctx, "model/list", params)
		if err != nil {
			return chatmodel.CodexCatalog{}, err
		}
		data, _ := json.Marshal(response["data"])
		var models []chatmodel.CodexModel
		if err := json.Unmarshal(data, &models); err != nil {
			return chatmodel.CodexCatalog{}, fmt.Errorf("invalid Codex model catalog: %w", err)
		}
		for _, model := range models {
			if model.Model == "" || model.Hidden || seen[model.Model] || chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{Model: model.Model, ReasoningEffort: model.DefaultEffort}) != nil {
				continue
			}
			seen[model.Model] = true
			validEfforts := make([]chatmodel.ReasoningOption, 0, len(model.Efforts))
			for _, effort := range model.Efforts {
				if effort.Effort != "" && chatmodel.ValidateCodexOptions(chatmodel.CodexOptions{ReasoningEffort: effort.Effort}) == nil {
					validEfforts = append(validEfforts, effort)
				}
			}
			model.Efforts = validEfforts
			model.DisplayName = chatmodel.Clip(model.DisplayName, 180)
			if model.DisplayName == "" {
				model.DisplayName = model.Model
			}
			catalog.Models = append(catalog.Models, model)
			if catalog.DefaultModel == "" && model.IsDefault {
				catalog.DefaultModel = model.Model
			}
		}
		next := stringValue(response["nextCursor"])
		if next == "" {
			r.catalog, r.catalogKey, r.catalogUntil = catalog, key, time.Now().Add(time.Minute)
			r.catalogErr = nil
			return catalog.Clone(), nil
		}
		if next == cursor {
			return chatmodel.CodexCatalog{}, fmt.Errorf("Codex model catalog repeated its pagination cursor")
		}
		cursor = next
	}
	return chatmodel.CodexCatalog{}, fmt.Errorf("Codex model catalog exceeds the pagination limit")
}
