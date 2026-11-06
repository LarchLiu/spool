package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func protocolResult(t *testing.T, protocol approvalProtocol, response chatmodel.ApprovalResponse, want string) {
	t.Helper()
	normalized, err := protocol.request.NormalizeResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	got := protocol.result(normalized)
	if !reflect.DeepEqual(got, elicitationParams(t, want)) {
		data, _ := json.Marshal(got)
		t.Fatalf("result=%s want=%s", data, want)
	}
}

func TestCodexAdvertisedApprovalDecisionsRoundTripExactly(t *testing.T) {
	params := elicitationParams(t, `{"availableDecisions":["acceptForSession","accept","decline","cancel",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","fetch"]}},{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"deny","host":"example.com"}}}]}`)
	protocol, ok := codexApproval("item/commandExecution/requestApproval", params)
	if !ok || len(protocol.request.Decisions) != 6 {
		t.Fatal(protocol.request)
	}
	for i, decision := range protocol.request.Decisions {
		response, err := protocol.request.NormalizeResponse(chatmodel.ApprovalResponse{Decision: decision.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := protocol.result(response)["decision"]; !reflect.DeepEqual(got, params["availableDecisions"].([]any)[i]) {
			t.Fatalf("decision changed: %#v", got)
		}
	}
	limited, _ := codexApproval("item/commandExecution/requestApproval", elicitationParams(t, `{"availableDecisions":["acceptForSession","cancel"]}`))
	for _, response := range []chatmodel.ApprovalResponse{{Allow: true}, {Decision: "policy-999"}, {Decision: "persist-always"}} {
		if limited.request.ValidateResponse(response) == nil {
			t.Fatal("invented/unadvertised decision was accepted")
		}
	}
	protocol, _ = codexApproval("item/fileChange/requestApproval", map[string]any{"grantRoot": "/work"})
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "session"}, `{"decision":"acceptForSession"}`)
}

func TestCodexPermissionsOnlyReturnSelectedGrantsAndPreserveDenyEntries(t *testing.T) {
	params := elicitationParams(t, `{"permissions":{"network":{"enabled":true},"fileSystem":{"read":["/read"],"write":["/write"],"entries":[{"access":"read","path":{"type":"path","path":"/other"}},{"access":"deny","path":{"type":"path","path":"/other/secret"}}],"globScanMaxDepth":2}}}`)
	protocol := codexPermissionApproval(params)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "session", Content: map[string]any{"permissions": []any{"grant-3"}, "strict_auto_review": true}}, `{"permissions":{"fileSystem":{"entries":[{"access":"read","path":{"type":"path","path":"/other"}},{"access":"deny","path":{"type":"path","path":"/other/secret"}}],"globScanMaxDepth":2}},"scope":"session","strictAutoReview":true}`)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "allow", Content: map[string]any{"permissions": []any{}}}, `{"permissions":{},"scope":"turn"}`)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{}, `{"permissions":{},"scope":"turn"}`)
	for _, content := range []map[string]any{{"permissions": []any{"/arbitrary"}}, {"permissions": []any{"grant-1", "grant-1"}}, {"scope": "session"}} {
		if protocol.request.ValidateResponse(chatmodel.ApprovalResponse{Decision: "allow", Content: content}) == nil {
			t.Fatal("permission expansion accepted", content)
		}
	}
}

func TestCodexUserInputSupportsChoicesOtherAndSecretAnswers(t *testing.T) {
	protocol, _ := codexApproval("item/tool/requestUserInput", elicitationParams(t, `{"questions":[{"id":"region","question":"Where?","options":[{"label":"EU","description":"European region"}],"isOther":true},{"id":"token","question":"Token?","isSecret":true}]}`))
	if protocol.request.Kind != "input" || !protocol.request.Fields[1].Secret || !protocol.request.Fields[0].AllowOther || protocol.request.Fields[0].Options[0].Description != "European region" {
		t.Fatal(protocol.request)
	}
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "allow", Content: map[string]any{"region": "APAC", "token": "secret-value"}}, `{"answers":{"region":{"answers":["APAC"]},"token":{"answers":["secret-value"]}}}`)
	if protocol.request.ValidateResponse(chatmodel.ApprovalResponse{Decision: "allow"}) == nil {
		t.Fatal("missing answers accepted")
	}
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "deny"}, `{"answers":{}}`)
}

func TestMCPElicitationPersistenceAndSchemaFallback(t *testing.T) {
	params := elicitationParams(t, `{"mode":"openai/form","requestedSchema":{"type":"object","properties":{"nested":{"type":"object","properties":{"name":{"type":"string","pattern":"^ok$"}},"required":["name"],"additionalProperties":false}},"required":["nested"],"additionalProperties":false},"_meta":{"persist":["session","always"]}}`)
	protocol, _ := codexApproval("mcpServer/elicitation/request", params)
	if !protocol.request.JSONForm || protocol.request.Unavailable != "" {
		t.Fatal(protocol.request)
	}
	content := map[string]any{"nested": map[string]any{"name": "ok"}}
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "persist-always", Content: content}, `{"action":"accept","content":{"nested":{"name":"ok"}},"_meta":{"persist":"always"}}`)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "cancel"}, `{"action":"cancel","content":null}`)
	if protocol.request.ValidateResponse(chatmodel.ApprovalResponse{Decision: "allow", Content: map[string]any{"nested": map[string]any{"name": "bad"}}}) == nil {
		t.Fatal("nested constraints were ignored")
	}
	delete(params, "_meta")
	limited, _ := codexApproval("mcpServer/elicitation/request", params)
	if limited.request.ValidateResponse(chatmodel.ApprovalResponse{Decision: "persist-always", Content: content}) == nil {
		t.Fatal("unadvertised persistence accepted")
	}
	request := codexElicitation(elicitationParams(t, `{"mode":"form","requestedSchema":{"$ref":"file:///etc/passwd"}}`))
	if request.Unavailable == "" {
		t.Fatal("external schema read was permitted")
	}
}

func TestClaudePermissionSuggestionsInputEditsAndDenialFeedback(t *testing.T) {
	body := elicitationParams(t, `{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-1","input":{"command":"echo original"},"decision_reason":"\u001b[31mNeeds permission\u001b[0m","permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"echo:*"}],"behavior":"allow","destination":"projectSettings"},{"type":"setMode","mode":"default","destination":"session"}]}`)
	protocol, _ := claudeApproval(body)
	if strings.Contains(protocol.request.Reason, "\x1b") || len(protocol.request.Decisions) != 5 {
		t.Fatal(protocol.request)
	}
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "suggestion-0", Content: map[string]any{"input": map[string]any{"command": "echo edited"}}}, `{"behavior":"allow","updatedInput":{"command":"echo edited"},"toolUseID":"tool-1","updatedPermissions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"echo:*"}],"behavior":"allow","destination":"projectSettings"}]}`)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "cancel", Feedback: "Use a read-only command"}, `{"behavior":"deny","interrupt":true,"message":"Use a read-only command","toolUseID":"tool-1"}`)
	body["suppress_always_allow_rule"] = true
	protocol, _ = claudeApproval(body)
	if len(protocol.request.Decisions) != 4 || protocol.request.ValidateResponse(chatmodel.ApprovalResponse{Decision: "suggestion-0"}) == nil {
		t.Fatal("suppressed persistent suggestion exposed")
	}
}

func TestClaudeQuestionsAndElicitation(t *testing.T) {
	protocol, _ := claudeApproval(elicitationParams(t, `{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"q1","input":{"questions":[{"question":"Features?","multiSelect":true,"options":[{"label":"A"},{"label":"B"}]},{"question":"Count?","kind":"number","min":1,"max":5}]}}`))
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "allow", Content: map[string]any{"Features?": []any{"A", "custom"}, "Count?": float64(3)}}, `{"behavior":"allow","toolUseID":"q1","updatedInput":{"questions":[{"question":"Features?","multiSelect":true,"options":[{"label":"A"},{"label":"B"}]},{"question":"Count?","kind":"number","min":1,"max":5}],"answers":{"Features?":"A, custom","Count?":"3"}}}`)
	protocol, _ = claudeApproval(elicitationParams(t, `{"subtype":"elicitation","mcp_server_name":"forms","message":"Choose","requested_schema":{"type":"object","properties":{"a":{"type":"boolean"},"z":{"type":"string"}}},"property_order":["z","a"]}`))
	if protocol.request.Fields[0].Name != "z" {
		t.Fatal("property order lost")
	}
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "cancel"}, `{"action":"cancel","content":null}`)
}

func TestClaudeOnlyDeclaresDialogsItCanComplete(t *testing.T) {
	kinds := claudeInitializeRequest()["supportedDialogKinds"].([]string)
	for _, kind := range kinds {
		payload := map[string]any{}
		if kind == "auto_mode_outside_reads" {
			payload = elicitationParams(t, `{"options":[{"value":"allow_once","label":"Once"},{"value":"ask_again","label":"Ask again"}]}`)
		}
		if strings.HasPrefix(kind, "mcp_") && kind != "mcp_elicitation_waiting" {
			payload = elicitationParams(t, `{"serverName":"test","params":{"mode":"form","requestedSchema":{}}}`)
		}
		protocol, ok := claudeDialog(map[string]any{"dialog_kind": kind, "payload": payload})
		if !ok {
			t.Fatalf("declared dialog not implemented: %s", kind)
		}
		for _, decision := range protocol.request.Decisions {
			if _, err := protocol.request.NormalizeResponse(chatmodel.ApprovalResponse{Decision: decision.ID}); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
		}
	}
	protocol, _ := claudeDialog(elicitationParams(t, `{"dialog_kind":"refusal_fallback_prompt","payload":{"originalModel":"a","fallbackModel":"b"}}`))
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "retry_fallback"}, `{"behavior":"completed","result":"retry_fallback"}`)
	writer := &approvalWriter{}
	p := &agentProcess{ctx: context.Background(), stdin: writer}
	if err := p.handleClaudeApproval(context.Background(), elicitationParams(t, `{"request_id":"other","request":{"subtype":"request_user_dialog","dialog_kind":"unknown-native-dialog"}}`)); err != nil || writer.Len() != 0 {
		t.Fatal("unknown dialog falsely dismissed", writer.String(), err)
	}
}

func TestLegacyCodexApprovalKeepsSessionAndInterruptSemantics(t *testing.T) {
	protocol, _ := codexApproval("execCommandApproval", elicitationParams(t, `{"command":["ls"],"conversationId":"thread-1"}`))
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "session"}, `{"decision":"approved_for_session"}`)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "deny", Feedback: "No network"}, `{"decision":{"denied":{"rejection":"No network"}}}`)
	protocolResult(t, protocol, chatmodel.ApprovalResponse{Decision: "cancel"}, `{"decision":"abort"}`)
}

func TestClaudeDialogAcrossPipeRetractsOnlyAfterSettlement(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Prompt = "refusal-dialog"
	request.Approve = func(_ context.Context, approval chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
		if approval.Title != "Choose how to continue" {
			t.Errorf("wrong dialog: %#v", approval)
		}
		return chatmodel.ApprovalResponse{Decision: "retry_fallback"}, nil
	}
	result, events := runFixture(t, newTestRunner(t), request)
	if strings.Contains(result.Text, "Retracted refusal") {
		t.Fatal("retracted message survived", result.Text)
	}
	seen := false
	for _, event := range events {
		if event.Type == "retract" && reflect.DeepEqual(event.RetractedIDs, []string{"refused-message"}) {
			seen = true
		}
	}
	if !seen || fixtureCount(t, request, "dialog-reply") != 1 {
		t.Fatal("dialog did not settle and retract through the CLI pipe")
	}
}
