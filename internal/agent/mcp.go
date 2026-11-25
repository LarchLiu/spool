package agent

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

func requestMCP(request AgentRequest) MCPServer {
	if request.MCP == nil || request.SummaryOnly {
		return MCPServer{}
	}
	return *request.MCP
}

func validateMCP(server MCPServer) error {
	if server == (MCPServer{}) {
		return nil
	}
	u, err := url.Parse(server.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/mcp" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(server.Token) != 43 {
		return errors.New("Invalid application MCP endpoint")
	}
	return nil
}

func claudeMCPConfig(server MCPServer) (string, func(), error) {
	base := filepath.Join(os.TempDir(), "spool-"+strconv.Itoa(os.Getuid()), "spool")
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", nil, err
	}
	directory, err := os.MkdirTemp(base, "agent-mcp.")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	body, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"spool_federation": map[string]any{"type": "http", "url": server.URL, "headers": map[string]string{"Authorization": "Bearer " + server.Token}}}})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	path := filepath.Join(directory, "mcp.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
