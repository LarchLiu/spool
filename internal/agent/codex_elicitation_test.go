package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func elicitationParams(t *testing.T, source string) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal([]byte(source), &params); err != nil {
		t.Fatal(err)
	}
	return params
}

type approvalWriter struct{ bytes.Buffer }

func (*approvalWriter) Close() error { return nil }

func TestCodexMCPElicitationReachesApprovalAndReplies(t *testing.T) {
	for _, test := range []struct {
		name, params string
		response     chatmodel.ApprovalResponse
		want         string
	}{
		{"computer-use", `{"mode":"form","message":"Allow Computer Use to use Calculator?","requestedSchema":{"type":"object","properties":{}},"_meta":{"connector_name":"Computer Use","tool_params_display":[{"display_name":"App","value":"Calculator"}]}}`, chatmodel.ApprovalResponse{Allow: true}, `{"action":"accept","content":{}}`},
		{"extended-form", `{"mode":"openai/form","message":"Allow Computer Use?","requestedSchema":{}}`, chatmodel.ApprovalResponse{Allow: true}, `{"action":"accept","content":{}}`},
		{"form-values", `{"mode":"form","requestedSchema":{"type":"object","properties":{"allow":{"type":"boolean"},"name":{"type":"string","minLength":1},"count":{"type":"integer","minimum":1},"apps":{"type":"array","items":{"enum":["calc","notes"],"type":"string"}}},"required":["allow","name","count","apps"]}}`, chatmodel.ApprovalResponse{Allow: true, Content: map[string]any{"allow": false, "name": "Alex", "count": float64(2), "apps": []any{"calc"}}}, `{"action":"accept","content":{"allow":false,"name":"Alex","count":2,"apps":["calc"]}}`},
		{"url", `{"mode":"url","message":"Sign in","url":"https://example.com/authorize","elicitationId":"url-1"}`, chatmodel.ApprovalResponse{Allow: true}, `{"action":"accept","content":null}`},
		{"deny", `{"mode":"form","requestedSchema":{"type":"object","properties":{"confirm":{"type":"boolean"}},"required":["confirm"]}}`, chatmodel.ApprovalResponse{}, `{"action":"decline","content":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := elicitationParams(t, test.params)
			params["threadId"], params["serverName"] = "thread-1", "computer-use"
			writer := &approvalWriter{}
			calls := 0
			p := &agentProcess{sessionID: "thread-1", ctx: context.Background(), stdin: writer, messages: make(chan map[string]any)}
			p.approve = func(_ context.Context, request chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
				calls++
				if request.Unavailable != "" || request.Title == "" {
					t.Errorf("request not actionable: %#v", request)
				}
				if test.name == "computer-use" && (request.Title != "Computer Use" || !strings.Contains(request.Detail, "Calculator") || request.Reason != params["message"]) {
					t.Errorf("app approval lost its prompt: %#v", request)
				}
				return test.response, nil
			}
			if err := p.handleCodexApproval(context.Background(), map[string]any{"id": "request-1", "method": "mcpServer/elicitation/request", "params": params}, "turn-1"); err != nil {
				t.Fatal(err)
			}
			var reply map[string]any
			if err := json.Unmarshal(writer.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || reply["id"] != "request-1" || !reflect.DeepEqual(reply["result"], elicitationParams(t, test.want)) {
				t.Fatalf("wrong approval response: %s (calls=%d)", writer.String(), calls)
			}
		})
	}
}

func TestCodexMCPElicitationRejectsOtherThreadOrTurn(t *testing.T) {
	for _, params := range []string{
		`{"threadId":"other","mode":"form","requestedSchema":{}}`,
		`{"threadId":"thread-1","turnId":"old-turn","mode":"form","requestedSchema":{}}`,
	} {
		writer := &approvalWriter{}
		p := &agentProcess{sessionID: "thread-1", ctx: context.Background(), stdin: writer, approve: func(context.Context, chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
			t.Error("unrelated approval reached the user")
			return chatmodel.ApprovalResponse{Allow: true}, nil
		}}
		if err := p.handleCodexApproval(context.Background(), map[string]any{"id": 1, "method": "mcpServer/elicitation/request", "params": elicitationParams(t, params)}, "turn-1"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(writer.String(), `"action":"decline"`) {
			t.Fatal(writer.String())
		}
	}
}

func TestCodexMCPElicitationResolutionClearsPromptWithoutReply(t *testing.T) {
	writer := &approvalWriter{}
	started, stopped := make(chan struct{}), make(chan struct{})
	p := &agentProcess{sessionID: "thread-1", ctx: context.Background(), stdin: writer, messages: make(chan map[string]any, 1)}
	p.approve = func(ctx context.Context, _ chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return chatmodel.ApprovalResponse{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		done <- p.handleCodexApproval(context.Background(), map[string]any{"id": "request-1", "method": "mcpServer/elicitation/request", "params": elicitationParams(t, `{"threadId":"thread-1","mode":"form","requestedSchema":{}}`)}, "turn-1")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("approval never started")
	}
	p.messages <- map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "thread-1", "requestId": "request-1"}}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("resolved approval remained pending")
	}
	if writer.Len() != 0 {
		t.Fatalf("already resolved request received another reply: %s", writer.String())
	}
}

func TestCodexMCPElicitationUnsupportedInputRemainsVisible(t *testing.T) {
	for _, params := range []string{
		`{"mode":"openai/userVerification","title":"Verify device","description":"Use your device","challenge":"secret"}`,
		`{"mode":"form","requestedSchema":{"type":"object","properties":{"nested":{"type":"object"}}}}`,
		`{"mode":"url","url":"javascript:alert(1)"}`,
	} {
		request := codexElicitation(elicitationParams(t, params))
		if request.Unavailable == "" || request.ValidateResponse(chatmodel.ApprovalResponse{Allow: true}) == nil || request.ValidateResponse(chatmodel.ApprovalResponse{}) != nil {
			t.Fatalf("unsupported request could be silently granted: %#v", request)
		}
	}
}

func TestCodexMCPElicitationAcrossCLIPipe(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "mcp-elicitation"
	data := `{"mode":"openai/form","message":"Allow Computer Use to use Calculator?","requestedSchema":{"type":"object","properties":{"confirm":{"type":"boolean"}},"required":["confirm"]},"_meta":{"connector_name":"Computer Use"}}`
	if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "fixture-elicitation.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	request.Approve = func(_ context.Context, approval chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
		calls++
		if len(approval.Fields) != 1 || approval.Title != "Computer Use" {
			t.Errorf("wrong approval: %#v", approval)
		}
		return chatmodel.ApprovalResponse{Allow: true, Content: map[string]any{"confirm": true}}, nil
	}
	runFixture(t, newTestRunner(t), request)
	if calls != 1 || fixtureCount(t, request, "elicitation-reply") != 1 {
		t.Fatal("MCP request did not complete through the CLI")
	}
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		if entry["method"] == "elicitation-reply" {
			result, _ := value["result"].(map[string]any)
			if !reflect.DeepEqual(result, elicitationParams(t, `{"action":"accept","content":{"confirm":true}}`)) {
				t.Fatalf("form response changed on the pipe: %#v", value)
			}
		}
		if entry["method"] == "initialize" {
			params, _ := value["params"].(map[string]any)
			capabilities, _ := params["capabilities"].(map[string]any)
			if capabilities["mcpServerOpenaiFormElicitation"] != true {
				t.Fatal("extended elicitation not advertised")
			}
		}
	}
}
