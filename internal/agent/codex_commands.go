package agent

import "context"

func (p *agentProcess) codexPlan(ctx context.Context, request AgentRequest) error {
	model := request.Model
	if model == "" {
		model = p.model
	}
	settings := map[string]any{
		"threadId": p.sessionID,
		"collaborationMode": map[string]any{
			"mode":     "plan",
			"settings": map[string]any{"model": model, "reasoning_effort": nil, "developer_instructions": nil},
		},
	}
	_, err := p.codexRPC(ctx, "thread/settings/update", settings)
	return err
}
