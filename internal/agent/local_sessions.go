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
			return nil, err
		}
		folder := "sessions"
		if backend == chatmodel.BackendClaude {
			folder = "projects"
		}
		err = filepath.WalkDir(filepath.Join(home, folder), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
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
				cached = localSessionFile{info: info, session: readLocalSession(backend, path, info)}
			}
			files[path] = cached
			v := cached.session
			if v.ID == "" || v.Directory == "" {
				return nil
			}
			if backend == chatmodel.BackendCodex {
				if title := index.titles.Title(path, v.ID); title != "" {
					v.Title = title
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

func readLocalSession(backend chatmodel.Backend, path string, info os.FileInfo) LocalSession {
	v := LocalSession{Path: path, Backend: backend, UpdatedAt: info.ModTime().UnixMilli(), Generation: chatmodel.NewID()}
	file, err := os.Open(path)
	if err != nil {
		return v
	}
	defer file.Close()
	var title nativeSessionTitle
	collect := func(reader io.Reader, identity bool) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 256*1024)
		for scanner.Scan() {
			var record map[string]any
			if json.Unmarshal(scanner.Bytes(), &record) != nil {
				continue
			}
			title.collect(backend, record)
			if !identity {
				continue
			}
			if record["isSidechain"] == true {
				v.ID, v.Directory = "", ""
				return
			}
			if backend == chatmodel.BackendCodex && record["type"] == "session_meta" {
				payload, _ := record["payload"].(map[string]any)
				if source, ok := payload["source"].(map[string]any); ok && source["subagent"] != nil {
					v.ID, v.Directory = "", ""
					return
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
	}
	// Discovery never decodes whole conversations. Titles near the end are
	// sampled separately; full history is read lazily by the existing cache.
	collect(io.LimitReader(file, 256*1024), true)
	if info.Size() > 256*1024 {
		if _, err := file.Seek(max(0, info.Size()-64*1024), io.SeekStart); err == nil {
			reader := bufio.NewReader(file)
			_, _ = reader.ReadBytes('\n')
			collect(io.LimitReader(reader, 64*1024), false)
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
	return v
}
