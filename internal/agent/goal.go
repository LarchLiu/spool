package agent

import (
	"context"
	"encoding/json"
	"fmt"

	chatmodel "spool/internal/model"
)

func (p *agentProcess) codexGoal(ctx context.Context, command *chatmodel.GoalCommand) (string, error) {
	params := map[string]any{"threadId": p.sessionID}
	if command.Action != "get" {
		params["origin"] = "user"
	}
	if command.Action == "set" {
		params["objective"], params["status"], params["origin"] = command.Objective, "active", "user"
	}
	response, err := p.codexRPC(ctx, "thread/goal/"+command.Action, params)
	if err != nil {
		return "", fmt.Errorf("Native Codex /goal failed; check CLI support and policy (no prompt fallback): %w", err)
	}
	if command.Action == "clear" {
		return "Goal cleared.", nil
	}
	goal, exists := response["goal"]
	if !exists {
		return "", fmt.Errorf("Codex returned no native goal state")
	}
	if goal == nil {
		if command.Action == "set" {
			return "", fmt.Errorf("Codex did not save the native goal")
		}
		return "No goal set.", nil
	}
	if command.Action == "set" {
		state, ok := goal.(map[string]any)
		if !ok || state["objective"] != command.Objective || state["status"] != "active" {
			return "", fmt.Errorf("Codex did not apply the requested native goal")
		}
	}
	body, err := json.MarshalIndent(goal, "", "  ")
	return string(body), err
}
