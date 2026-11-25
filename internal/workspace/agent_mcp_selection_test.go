package workspace

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestAgentMCPRequiresExplicitSelectionOnEachWebMessage(t *testing.T) {
	f := remoteFixtureForTest(t, &fakeRunner{text: "remote"})
	testAgent(t, f.a.store)
	cookie, csrf := loginRequest(t, f.a, f.identity.User.Username)
	runner := f.a.runner.(*fakeRunner)
	for index, selection := range []string{"", `,"mcp_servers":["spool_federation"]`, `,"mcp_servers":[]`, ""} {
		chatID := f.origin
		if index == 3 {
			parent, _ := f.a.store.getSession(f.origin)
			chat, err := f.a.store.CreateChat(f.identity.User.ID, parent.ProjectID, chatmodel.BackendCodex)
			if err != nil {
				t.Fatal(err)
			}
			chatID = chat.ID
		}
		response := apiRequest(f.a, "POST", "/api/chats/"+chatID+"/messages", `{"message":"Use remote tools if selected"`+selection+`}`, cookie, csrf)
		if response.Code != 202 {
			t.Fatal(response.Code, response.Body.String())
		}
		f.a.turns.Wait()
		runner.mu.Lock()
		requests := append([]agent.AgentRequest(nil), runner.requests...)
		runner.mu.Unlock()
		if len(requests) != index+1 || (requests[index].MCP != nil) != (index == 1) {
			t.Fatalf("MCP selection leaked between messages/chats at step %d: %+v", index, requests)
		}
		if requests[index].Prompt != "Use remote tools if selected" {
			t.Fatal("MCP selector changed the message body")
		}
	}
	// Direct user @ calls do not depend on any MCP selection.
	body := f.call(t, "delegate", federationAgentPayload{Project: f.project, Backend: chatmodel.BackendCodex, Message: "Direct user task", RequestID: chatmodel.NewID()})
	if !bytes.Contains(body, []byte(`"status":"completed"`)) {
		t.Fatal("unselected MCP blocked direct delegation", string(body))
	}
}

func TestAgentMCPSelectionRejectsUnknownAndDuplicateServers(t *testing.T) {
	f := remoteFixtureForTest(t, &fakeRunner{text: "remote"})
	cookie, csrf := loginRequest(t, f.a, f.identity.User.Username)
	for _, value := range []string{`["other"]`, `["spool_federation","spool_federation"]`, `"spool_federation"`, `["http://other/mcp"]`} {
		response := apiRequest(f.a, "POST", "/api/chats/"+f.origin+"/messages", `{"message":"task","mcp_servers":`+value+`}`, cookie, csrf)
		if response.Code != 400 {
			t.Fatal("invalid MCP selection accepted", value, response.Code, response.Body.String())
		}
	}
	runner := f.a.runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.requests) != 0 {
		t.Fatal("invalid selections started an agent")
	}
}

func TestAgentMCPSelectionMultipartContract(t *testing.T) {
	for _, value := range []string{"", `[]`, `["spool_federation"]`, `"spool_federation"`, `["other"]`, `["spool_federation","spool_federation"]`} {
		t.Run(value, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			if err := writer.WriteField("message", "actual task"); err != nil {
				t.Fatal(err)
			}
			if value != "" {
				if err := writer.WriteField("mcp_servers", value); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/messages", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			input, ok := readMessageInput(response, request)
			valid := value == "" || value == `[]` || value == `["spool_federation"]`
			if ok != valid {
				t.Fatal("multipart selection validation", value, response.Body.String())
			}
			if ok && (input.Message != "actual task" || (len(input.MCPServers) != 0) != (value == `["spool_federation"]`)) {
				t.Fatal("multipart selection altered input", input)
			}
		})
	}
}
