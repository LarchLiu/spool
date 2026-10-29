package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

type browserCommand struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type browserDriver struct {
	profile    string
	mu         sync.Mutex
	connection *websocket.Conn
	nextID     int
}

func (driver *browserDriver) close() {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.connection != nil {
		driver.connection.CloseNow()
	}
}

func browserPage(ctx context.Context, profile string) (string, error) {
	port, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
	if err != nil {
		return "", err
	}
	address := "http://127.0.0.1:" + strings.SplitN(string(port), "\n", 2)[0]
	request, err := http.NewRequestWithContext(ctx, "GET", address+"/json/list", nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var targets []struct {
		Type string `json:"type"`
		URL  string `json:"webSocketDebuggerUrl"`
	}
	if err = json.NewDecoder(response.Body).Decode(&targets); err != nil {
		return "", err
	}
	var target string
	for _, entry := range targets {
		if entry.Type == "page" {
			target = entry.URL
			break
		}
	}
	if target == "" {
		return "", fmt.Errorf("browser page target unavailable")
	}
	return target, nil
}

// Keep the CDP session alive across requests so viewport overrides survive until
// explicitly cleared. Real input exercises pointer capture and hit testing.
func (driver *browserDriver) run(ctx context.Context, commands []browserCommand) (json.RawMessage, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.connection == nil {
		target, err := browserPage(ctx, driver.profile)
		if err != nil {
			return nil, err
		}
		connection, _, err := websocket.Dial(ctx, target, nil)
		if err != nil {
			return nil, err
		}
		connection.SetReadLimit(8 << 20)
		driver.connection = connection
	}
	connection := driver.connection
	var result json.RawMessage
	for _, command := range commands {
		driver.nextID++
		id := driver.nextID
		data, err := json.Marshal(map[string]any{"id": id, "method": command.Method, "params": command.Params})
		if err != nil {
			return nil, err
		}
		if err = connection.Write(ctx, websocket.MessageText, data); err != nil {
			return nil, err
		}
		for {
			_, data, err = connection.Read(ctx)
			if err != nil {
				return nil, err
			}
			var reply struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err = json.Unmarshal(data, &reply); err != nil {
				return nil, err
			}
			if reply.ID != id {
				continue
			}
			if len(reply.Error) > 0 {
				return nil, fmt.Errorf("%s: %s", command.Method, reply.Error)
			}
			result = reply.Result
			break
		}
	}
	return result, nil
}
