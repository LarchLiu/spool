package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

// CodexSessionLister uses the native app-server index, never a second filesystem
// discovery implementation. Binary is the optional configured CLI executable.
type CodexSessionLister struct{ Binary string }

func (l CodexSessionLister) ListSessions(ctx context.Context, options ListSessionsOptions) ([]LocalSession, error) {
	return l.listSessions(ctx, options, false)
}

func (l CodexSessionLister) ListArchivedSessions(ctx context.Context) ([]LocalSession, error) {
	return l.listSessions(ctx, ListSessionsOptions{}, true)
}

func (l CodexSessionLister) listSessions(ctx context.Context, options ListSessionsOptions, archived bool) ([]LocalSession, error) {
	binary := l.Binary
	var err error
	if binary == "" {
		binary, err = (&CLIRunner{}).Binary(chatmodel.BackendCodex)
	} else {
		binary, err = exec.LookPath(binary)
	}
	if err != nil {
		return nil, err
	}
	directory, err := WorkingDirectory("")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	process, err := startAgentProcess(ctx, AgentRequest{Backend: chatmodel.BackendCodex, Binary: binary, WorkingDirectory: directory})
	if err != nil {
		return nil, err
	}
	defer func() {
		// End of input lets the app-server finish any state or metadata write
		// before its process group is reaped; only a stuck server is killed.
		_ = process.stdin.Close()
		select {
		case <-process.done:
		case <-ctx.Done():
		case <-time.After(agentExitGracePeriod):
		}
		cancel()
		<-process.done
	}()
	if err := process.initializeCodex(ctx); err != nil {
		return nil, err
	}
	var sessions []LocalSession
	seen, cursors := map[string]bool{}, map[string]bool{}
	cursor, skipped := "", 0
	for {
		params := map[string]any{
			"limit": 100, "sortKey": "updated_at", "archived": archived,
			"modelProviders": []string{},
			// An empty sourceKinds defaults to cli/vscode and would lose sessions
			// created by other app-server clients. Subagents remain excluded.
			"sourceKinds": []string{"cli", "vscode", "exec", "appServer", "unknown"},
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if options.Directory != "" {
			params["cwd"] = options.Directory
		}
		response, err := process.codexRPC(ctx, "thread/list", params)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(response["data"])
		if err != nil {
			return nil, err
		}
		var threads []struct {
			ID, Path, Cwd, Name, Preview string
			CreatedAt, UpdatedAt         int64
			Ephemeral                    bool
			Source                       json.RawMessage
			GitInfo                      struct{ Branch string }
		}
		if string(data) == "null" || json.Unmarshal(data, &threads) != nil {
			return nil, fmt.Errorf("Codex thread/list returned invalid data")
		}
		for _, t := range threads {
			if t.Ephemeral || !chatmodel.ValidID(t.ID) || seen[t.ID] || codexSubagentSource(t.Source) {
				continue
			}
			seen[t.ID] = true
			if skipped < max(0, options.Offset) {
				skipped++
				continue
			}
			name := strings.Join(strings.Fields(t.Name), " ")
			title := name
			if title == "" {
				title = strings.Join(strings.Fields(t.Preview), " ")
			}
			if title == "" {
				title = chatmodel.BackendCodex.Label()
			}
			sessions = append(sessions, LocalSession{ID: t.ID, Path: t.Path, Directory: t.Cwd,
				Archived: archived,
				Title:    title, CustomTitle: t.Name, FirstPrompt: t.Preview, GitBranch: t.GitInfo.Branch,
				// Only a native name is confirmed when the chat is opened; a
				// preview must not replace a title read from complete history.
				Backend: chatmodel.BackendCodex, CreatedAt: t.CreatedAt * 1000, UpdatedAt: t.UpdatedAt * 1000, TitleAuthoritative: name != ""})
			if options.Limit > 0 && len(sessions) >= options.Limit {
				return sessions, nil
			}
		}
		next := stringValue(response["nextCursor"])
		if next == "" {
			return sessions, nil
		}
		if cursors[next] {
			return nil, fmt.Errorf("Codex thread/list repeated its pagination cursor")
		}
		cursors[next] = true
		cursor = next
	}
}

// codexSubagentSource inspects the structure of a thread source: its kind is
// either a string or a key of an object. Free-form values such as a custom
// client label never classify a top-level thread as a subagent.
func codexSubagentSource(source json.RawMessage) bool {
	subagent := func(kind string) bool {
		kind = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(kind))
		return strings.HasPrefix(kind, "subagent")
	}
	var kind string
	if json.Unmarshal(source, &kind) == nil {
		return subagent(kind)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(source, &fields) != nil {
		return false
	}
	for key := range fields {
		if subagent(key) {
			return true
		}
	}
	return false
}
