package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type AgentInfo struct {
	Backend       chatmodel.Backend      `json:"backend"`
	Enabled       bool                   `json:"enabled"`
	Executable    string                 `json:"-"`
	ResolvedPath  string                 `json:"-"`
	Installed     bool                   `json:"installed"`
	Version       string                 `json:"version"`
	Catalog       chatmodel.CodexCatalog `json:"catalog"`
	CheckedAt     int64                  `json:"checked_at"`
	LastSuccessAt int64                  `json:"last_success_at"`
	Error         string                 `json:"error,omitempty"`
}

// agentRecord keeps private executable paths in SQLite without exposing them over the chat API.
type agentRecord struct {
	AgentInfo
	Executable   string `json:"executable"`
	ResolvedPath string `json:"resolved_path"`
}

func (s *Store) Agents() ([]AgentInfo, error) {
	out := []AgentInfo{}
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		var body string
		err := s.db.QueryRow(`SELECT body FROM agents WHERE backend=?`, backend).Scan(&body)
		if err == sql.ErrNoRows {
			out = append(out, AgentInfo{Backend: backend, Enabled: true, Catalog: chatmodel.CodexCatalog{Models: []chatmodel.CodexModel{}}})
			continue
		}
		if err != nil {
			return nil, err
		}
		var v agentRecord
		if err = json.Unmarshal([]byte(body), &v); err != nil {
			return nil, err
		}
		v.AgentInfo.Executable = v.Executable
		v.AgentInfo.ResolvedPath = v.ResolvedPath
		out = append(out, v.AgentInfo)
	}
	return out, nil
}
func (s *Store) saveAgent(v AgentInfo) error {
	b, err := json.Marshal(agentRecord{v, v.Executable, v.ResolvedPath})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO agents VALUES(?,?) ON CONFLICT(backend) DO UPDATE SET body=excluded.body WHERE body<>excluded.body`, v.Backend, string(b))
	return err
}
func (s *Store) ConfigureAgent(backend chatmodel.Backend, enabled bool, path string) error {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if !backend.Valid() {
		return fmt.Errorf("invalid agent")
	}
	all, err := s.Agents()
	if err != nil {
		return err
	}
	for _, v := range all {
		if v.Backend == backend {
			if v.Executable != strings.TrimSpace(path) {
				v.Installed = false
				v.Error = "Executable changed; waiting for discovery"
			}
			v.Enabled = enabled
			v.Executable = strings.TrimSpace(path)
			return s.saveAgent(v)
		}
	}
	return nil
}

type Registry struct {
	store *Store
	mu    sync.Mutex
}

func NewRegistry(s *Store) *Registry { return &Registry{store: s} }
func (r *Registry) Refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	all, err := r.store.Agents()
	if err != nil {
		return err
	}
	runner := agent.NewCLIRunner()
	defer runner.Close()
	runner.ConfigureBinaries(agentBinaries(all))
	for _, v := range all {
		if err = ctx.Err(); err != nil {
			return err
		}
		v.CheckedAt = time.Now().UnixMilli()
		v.Error = ""
		path, e := runner.Binary(v.Backend)
		v.Installed = e == nil
		v.ResolvedPath = path
		if e == nil {
			version, probeErr := agent.Version(ctx, path)
			e = probeErr
			if e == nil {
				v.Version = version
			}
			if e == nil && v.Enabled {
				var catalog chatmodel.CodexCatalog
				if v.Backend == chatmodel.BackendCodex {
					catalog, e = runner.CodexModels(ctx)
				} else {
					catalog, e = agent.DiscoverClaudeModels(ctx, path)
				}
				if e == nil {
					v.Catalog = catalog
					v.LastSuccessAt = time.Now().UnixMilli()
				}
			}
		}
		if e != nil {
			v.Error = chatmodel.Clip(e.Error(), 1024)
		}
		// A slow probe must never overwrite a path or enabled flag edited in
		// the desktop while discovery was in progress.
		r.store.agentMu.Lock()
		latest, readErr := r.store.Agents()
		if readErr == nil {
			for _, current := range latest {
				if current.Backend == v.Backend && current.Executable == v.Executable && current.Enabled == v.Enabled {
					readErr = r.store.saveAgent(v)
				}
			}
		}
		r.store.agentMu.Unlock()
		if err = readErr; err != nil {
			return err
		}
	}
	return nil
}
func (r *Registry) Run(ctx context.Context) {
	_ = r.Refresh(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.Refresh(ctx)
		}
	}
}

func agentBinaries(agents []AgentInfo) map[chatmodel.Backend]string {
	binaries := make(map[chatmodel.Backend]string, len(agents))
	for _, info := range agents {
		binaries[info.Backend] = info.Executable
	}
	return binaries
}
