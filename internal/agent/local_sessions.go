package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	chatmodel "spool/internal/model"
)

type LocalSession struct {
	ID, Path, Directory, Title string
	Generation                 string
	Backend                    chatmodel.Backend
	CreatedAt, UpdatedAt       int64
	TitleAuthoritative         bool
}

type localSessionFile struct {
	info    os.FileInfo
	session LocalSession
}

// LocalSessionIndex retains metadata only, never native transcript bodies.
// Callers serialize discovery; unchanged files need only a stat on later scans.
type LocalSessionIndex struct {
	files  map[string]localSessionFile
	titles CodexSessionTitles
}

func (index *LocalSessionIndex) Discover() ([]LocalSession, error) {
	files := make(map[string]localSessionFile)
	var sessions []LocalSession
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		home, err := nativeSessionHome(backend)
		if err != nil {
			continue
		}
		folder := "sessions"
		if backend == chatmodel.BackendClaude {
			folder = "projects"
		}
		err = filepath.WalkDir(filepath.Join(home, folder), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// Optional native history must not block healthy providers or
				// normal project navigation when one directory is unreadable.
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == "subagents" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			cached, ok := index.files[path]
			if !ok || info.Size() != cached.info.Size() || !info.ModTime().Equal(cached.info.ModTime()) || !os.SameFile(info, cached.info) {
				session, err := readLocalSession(backend, path, info)
				if err != nil {
					// Retry transient read failures even if the file generation is unchanged.
					return nil
				}
				cached = localSessionFile{info: info, session: session}
			}
			files[path] = cached
			v := cached.session
			if v.ID == "" || v.Directory == "" {
				return nil
			}
			if backend == chatmodel.BackendCodex {
				if title := index.titles.Title(path, v.ID); title != "" {
					v.Title = title
					v.TitleAuthoritative = true
				}
			}
			sessions = append(sessions, v)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	index.files = files
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt != sessions[j].UpdatedAt {
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		}
		return sessions[i].Path < sessions[j].Path
	})
	return sessions, nil
}

func readLocalSession(backend chatmodel.Backend, path string, info os.FileInfo) (LocalSession, error) {
	v := LocalSession{Path: path, Backend: backend, UpdatedAt: info.ModTime().UnixMilli(), Generation: chatmodel.NewID()}
	file, err := os.Open(path)
	if err != nil {
		return v, err
	}
	defer file.Close()
	var title nativeSessionTitle
	collect := func(source io.Reader, limit int64, identity bool) (int64, error) {
		reader := bufio.NewReader(source)
		var sampled int64
		for sampled < limit || (identity && (v.ID == "" || v.Directory == "")) {
			line, err := reader.ReadBytes('\n')
			sampled += int64(len(line))
			if err != nil && err != io.EOF {
				return sampled, err
			}
			if len(line) == 0 {
				break
			}
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				continue
			}
			title.collect(backend, record)
			if !identity {
				if backend == chatmodel.BackendClaude && record["type"] == "custom-title" && strings.TrimSpace(stringValue(record["customTitle"])) != "" {
					v.TitleAuthoritative = true
				}
				continue
			}
			if record["isSidechain"] == true {
				v.ID, v.Directory = "", ""
				return sampled, nil
			}
			if backend == chatmodel.BackendCodex && record["type"] == "session_meta" {
				payload, _ := record["payload"].(map[string]any)
				if source, ok := payload["source"].(map[string]any); ok && source["subagent"] != nil {
					v.ID, v.Directory = "", ""
					return sampled, nil
				}
				v.ID, v.Directory = stringValue(payload["id"]), stringValue(payload["cwd"])
				v.CreatedAt = nativeTimestamp(record["timestamp"])
			} else if backend == chatmodel.BackendClaude {
				if id := backendSessionID(record); id != "" && v.ID == "" {
					v.ID = id
				}
				if cwd := stringValue(record["cwd"]); cwd != "" && v.Directory == "" {
					v.Directory = cwd
				}
				if v.CreatedAt == 0 {
					v.CreatedAt = nativeTimestamp(record["timestamp"])
				}
			}
			if v.Title == "" {
				payload, _ := record["payload"].(map[string]any)
				var content any
				if backend == chatmodel.BackendCodex && record["type"] == "response_item" && payload["role"] == "user" {
					content = payload["content"]
				} else if backend == chatmodel.BackendClaude && record["type"] == "user" && record["isMeta"] != true && record["isCompactSummary"] != true {
					message, _ := record["message"].(map[string]any)
					content = message["content"]
				}
				texts, images := userContentTexts(content)
				v.Title = strings.Join(strings.Fields(cleanUserTextBlocks(texts, images).Text), " ")
			}
		}
		return sampled, nil
	}
	// Sample complete records, extending the prefix until identity is known so
	// large images or metadata cannot hide it. Transcript bodies are not retained.
	sampled, err := collect(io.NewSectionReader(file, 0, info.Size()), 256*1024, true)
	if err != nil {
		return v, err
	}
	v.TitleAuthoritative = sampled == info.Size()
	if sampled < info.Size() {
		start := max(0, info.Size()-64*1024)
		reader := bufio.NewReader(io.NewSectionReader(file, start, info.Size()-start))
		if _, err := reader.ReadBytes('\n'); err != nil && err != io.EOF {
			return v, err
		}
		if _, err := collect(reader, 64*1024, false); err != nil {
			return v, err
		}
	}
	if title.text != "" {
		v.Title = title.text
	}
	v.Title = chatmodel.Clip(v.Title, 200)
	if v.Title == "" {
		v.Title = backend.Label()
	}
	if v.CreatedAt == 0 {
		v.CreatedAt = v.UpdatedAt
	}
	if !chatmodel.ValidID(v.ID) || !filepath.IsAbs(v.Directory) {
		v.ID, v.Directory = "", ""
	}
	return v, nil
}
