package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"

	chatmodel "spool/internal/model"
)

const claudeSessionSampleBytes = 64 * 1024

// ClaudeSessionLister implements the local-filesystem listSessions contract of
// @anthropic-ai/claude-agent-sdk 0.3.323 in Go. It enumerates project directories
// on demand and samples transcript heads/tails; it needs neither a CLI nor a JS
// runtime. SessionStore is an SDK-provided custom storage hook, not local history.
//
// The zero value reads every candidate. NewClaudeSessionLister additionally
// remembers results for unchanged transcripts, so a periodic refresh only
// stats files instead of re-reading and re-parsing every sample.
type ClaudeSessionLister struct{ cache *claudeSessionCache }

func NewClaudeSessionLister() ClaudeSessionLister {
	return ClaudeSessionLister{cache: &claudeSessionCache{entries: map[string]claudeCachedSession{}}}
}

type claudeSessionCandidate struct {
	path, id, project string
	mtime             int64
	verifyProject     bool
	info              os.FileInfo
}

type claudeFileStamp struct {
	exists bool
	size   int64
	mtime  time.Time
}

type claudeCachedSession struct {
	candidate              claudeSessionCandidate
	sidecar                claudeFileStamp
	programmatic, untitled bool
	session                LocalSession
	ok                     bool
}

type claudeSessionCache struct {
	mu      sync.Mutex
	entries map[string]claudeCachedSession
}

func claudeSidecarPath(path, sessionID string) string {
	return filepath.Join(filepath.Dir(path), sessionID, "custom-title.json")
}

func (l ClaudeSessionLister) read(candidate claudeSessionCandidate, options ListSessionsOptions) (LocalSession, bool) {
	if l.cache == nil {
		session, ok, _ := readClaudeSession(candidate, options)
		return session, ok
	}
	entry := claudeCachedSession{candidate: candidate, untitled: options.IncludeUntitled,
		programmatic: options.IncludeProgrammatic == nil || *options.IncludeProgrammatic}
	if info, err := os.Stat(claudeSidecarPath(candidate.path, candidate.id)); err == nil {
		entry.sidecar = claudeFileStamp{true, info.Size(), info.ModTime()}
	}
	l.cache.mu.Lock()
	cached, hit := l.cache.entries[candidate.path]
	l.cache.mu.Unlock()
	if hit && cached.sidecar == entry.sidecar && cached.programmatic == entry.programmatic && cached.untitled == entry.untitled &&
		cached.candidate.project == candidate.project && cached.candidate.verifyProject == candidate.verifyProject &&
		cached.candidate.info.Size() == candidate.info.Size() && cached.candidate.info.ModTime().Equal(candidate.info.ModTime()) &&
		os.SameFile(cached.candidate.info, candidate.info) {
		return cached.session, cached.ok
	}
	var stable bool
	entry.session, entry.ok, stable = readClaudeSession(candidate, options)
	l.cache.mu.Lock()
	if stable {
		l.cache.entries[candidate.path] = entry
	} else {
		// Retry read failures and results that depend on another transcript.
		delete(l.cache.entries, candidate.path)
	}
	l.cache.mu.Unlock()
	return entry.session, entry.ok
}

func (l ClaudeSessionLister) ListSessions(ctx context.Context, options ListSessionsOptions) ([]LocalSession, error) {
	home, err := nativeSessionHome(chatmodel.BackendClaude)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, "projects")
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var projects []string
	if options.Directory != "" {
		directory, err := filepath.Abs(options.Directory)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(directory); err == nil {
			directory = resolved
		}
		projects = append(projects, directory)
		if options.IncludeWorktrees == nil || *options.IncludeWorktrees {
			projects = append(projects, claudeWorktrees(ctx, directory)...)
		}
	}
	var candidates []claudeSessionCandidate
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dir.IsDir() {
			continue
		}
		project := ""
		verifyProject := false
		if len(projects) > 0 {
			for _, path := range projects {
				name := claudeEncodedDirectory(path)
				hashed := len(name) > 200 && claudeDirectoryPrefix(dir.Name(), name[:200]+"-")
				if claudePathEqual(name, dir.Name()) || hashed {
					project = path
					verifyProject = hashed
					break
				}
			}
			if project == "" {
				continue
			}
		}
		folder := filepath.Join(root, dir.Name())
		files, err := os.ReadDir(folder)
		if err != nil {
			continue // one unreadable optional project must not hide healthy ones
		}
		for _, file := range files {
			if file.Type()&os.ModeSymlink != 0 || file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), ".jsonl")
			if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
				continue
			}
			info, err := file.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			// Keep the on-disk spelling: it is the native ID that Spool records
			// for its own chats and the name of the sidecar directory.
			candidates = append(candidates, claudeSessionCandidate{path: filepath.Join(folder, file.Name()), id: id, project: project, mtime: info.ModTime().UnixMilli(), verifyProject: verifyProject, info: info})
		}
	}
	// Sort cheap file metadata before reading candidates. Filtered entries and
	// duplicates do not consume the caller's offset or limit.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime != candidates[j].mtime {
			return candidates[i].mtime > candidates[j].mtime
		}
		return candidates[i].id < candidates[j].id
	})
	if l.cache != nil && options.Directory == "" {
		// A complete enumeration also names every transcript that still exists.
		present := make(map[string]bool, len(candidates))
		for _, candidate := range candidates {
			present[candidate.path] = true
		}
		l.cache.mu.Lock()
		for path := range l.cache.entries {
			if !present[path] {
				delete(l.cache.entries, path)
			}
		}
		l.cache.mu.Unlock()
	}
	seen, skipped := map[string]bool{}, 0
	var sessions []LocalSession
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, ok := l.read(candidate, options)
		// Two project folders can hold the same ID in a different letter case.
		key := strings.ToLower(v.ID)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		if skipped < max(0, options.Offset) {
			skipped++
			continue
		}
		sessions = append(sessions, v)
		if options.Limit > 0 && len(sessions) >= options.Limit {
			break
		}
	}
	return sessions, nil
}

func claudeEncodedDirectory(path string) string {
	var b strings.Builder
	for _, c := range utf16.Encode([]rune(path)) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteByte(byte(c))
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func claudePathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func claudeDirectoryPrefix(name, prefix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
	}
	return strings.HasPrefix(name, prefix)
}

func claudeWorktrees(ctx context.Context, directory string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=", "worktree", "list", "--porcelain", "-z")
	cmd.Dir = directory
	body, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, field := range strings.Split(string(body), "\x00") {
		if path, ok := strings.CutPrefix(field, "worktree "); ok {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				paths = append(paths, resolved)
			}
		}
	}
	return paths
}

func claudeSamples(path string) (head, tail string, info os.FileInfo, err error) {
	// Never follow a transcript symlink. Discovery also rejects symlink entries.
	if info, err = os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return
	}
	file, openErr := os.Open(path)
	if openErr != nil {
		err = openErr
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	data, readErr := io.ReadAll(io.NewSectionReader(file, 0, claudeSessionSampleBytes))
	if readErr != nil {
		err = readErr
		return
	}
	head, tail = string(data), string(data)
	if info.Size() > claudeSessionSampleBytes {
		data, err = io.ReadAll(io.NewSectionReader(file, info.Size()-claudeSessionSampleBytes, claudeSessionSampleBytes))
		tail = string(data)
	}
	return
}

func claudeSampleRecords(sample string) []map[string]any {
	var records []map[string]any
	for _, line := range strings.Split(sample, "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record != nil {
			records = append(records, record)
		}
	}
	return records
}

func claudeLatestSessionString(records []map[string]any, typ, key string) string {
	for i := len(records) - 1; i >= 0; i-- {
		if typ == "" || records[i]["type"] == typ {
			if value := stringValue(records[i][key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func claudeSidecarTitle(path, sessionID string) string {
	var sidecar struct{ CustomTitle string }
	body, err := os.ReadFile(claudeSidecarPath(path, sessionID))
	if err != nil || json.Unmarshal(body, &sidecar) != nil {
		return ""
	}
	return strings.TrimSpace(sidecar.CustomTitle)
}

// ClaudeSessionTitle applies current rename metadata to a title obtained from
// complete history. Tail custom titles take precedence over the sidecar, which
// supersedes older transcript titles. Sidecar changes do not invalidate history.
func ClaudeSessionTitle(path, sessionID, transcriptTitle string) string {
	sidecar := claudeSidecarTitle(path, sessionID)
	if sidecar == "" {
		return transcriptTitle
	}
	_, tail, _, err := claudeSamples(path)
	if err != nil {
		return transcriptTitle
	}
	if title := claudeLatestSessionString(claudeSampleRecords(tail), "custom-title", "customTitle"); title != "" {
		return title
	}
	return sidecar
}

// Claude's SDK extracts string fields even from a truncated record. Decode only
// the JSON string, so a large image/content record cannot hide cwd or timestamps.
func claudeSampleString(sample, key string, last bool) string {
	marker, value := "\""+key+"\"", ""
	for from := 0; from < len(sample); {
		i := strings.Index(sample[from:], marker)
		if i < 0 {
			break
		}
		i += from + len(marker)
		from = i
		rest := strings.TrimLeft(sample[i:], " \t\r")
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		rest = strings.TrimLeft(rest[1:], " \t\r")
		if !strings.HasPrefix(rest, "\"") {
			continue
		}
		for j := 1; j < len(rest); j++ {
			if rest[j] == '\\' {
				j++
				continue
			}
			if rest[j] == '"' {
				var decoded string
				if json.Unmarshal([]byte(rest[:j+1]), &decoded) == nil {
					value = decoded
					if !last {
						return value
					}
				}
				break
			}
		}
	}
	return value
}

// readClaudeSession also reports whether its result depends only on this
// transcript and its sidecar, and so may be reused until either changes.
func readClaudeSession(candidate claudeSessionCandidate, options ListSessionsOptions) (session LocalSession, ok, stable bool) {
	head, tail, info, err := claudeSamples(candidate.path)
	if err != nil || info == nil {
		return LocalSession{}, false, false
	}
	if head == "" {
		return LocalSession{}, false, true
	}
	first, _, _ := strings.Cut(head, "\n")
	var initial map[string]any
	_ = json.Unmarshal([]byte(first), &initial)
	if initial["isSidechain"] == true || strings.Contains(first, `"isSidechain":true`) || strings.Contains(first, `"isSidechain": true`) {
		return LocalSession{}, false, true
	}
	if options.IncludeProgrammatic != nil && !*options.IncludeProgrammatic {
		entrypoint := claudeSampleString(head, "entrypoint", false)
		if entrypoint == "" {
			entrypoint = claudeSampleString(tail, "entrypoint", true)
		}
		kind := claudeSampleString(head, "sessionKind", false)
		if entrypoint == "sdk-cli" || entrypoint == "sdk-ts" || entrypoint == "sdk-py" || kind == "daemon" || kind == "daemon-worker" {
			return LocalSession{}, false, true
		}
	}
	hrecords, trecords := claudeSampleRecords(head), claudeSampleRecords(tail)
	latest := claudeLatestSessionString
	custom := latest(trecords, "custom-title", "customTitle")
	authoritative := info.Size() <= claudeSessionSampleBytes || custom != ""
	if custom == "" {
		custom = claudeSidecarTitle(candidate.path, candidate.id)
		authoritative = authoritative || custom != ""
	}
	if custom == "" {
		custom = latest(hrecords, "custom-title", "customTitle")
	}
	if custom == "" {
		// An AI title cannot rule out a higher-priority custom title in the
		// unsampled middle. Only complete history can confirm that fallback.
		custom = latest(trecords, "ai-title", "aiTitle")
	}
	if custom == "" {
		custom = latest(hrecords, "ai-title", "aiTitle")
	}
	prompt, media, commandFallback := "", "", ""
	for _, record := range hrecords {
		var blockMedia string
		prompt, blockMedia = claudeListUserPrompt(record, &commandFallback)
		if media == "" {
			media = blockMedia
		}
		if prompt != "" {
			break
		}
	}
	if prompt == "" {
		prompt = commandFallback
	}
	if media == "" {
		for _, line := range strings.Split(head, "\n") {
			if !(strings.Contains(line, `"type":"user"`) || strings.Contains(line, `"type": "user"`)) || strings.Contains(line, `"tool_result"`) || strings.Contains(line, `"isMeta":true`) || strings.Contains(line, `"isMeta": true`) {
				continue
			}
			if strings.Contains(line, `"type":"image"`) || strings.Contains(line, `"type": "image"`) {
				media = "Image"
				break
			}
			if strings.Contains(line, `"type":"document"`) || strings.Contains(line, `"type": "document"`) {
				media = "Document"
				break
			}
		}
	}
	title := custom
	if title == "" {
		title = latest(trecords, "", "lastPrompt")
	}
	if custom == "" && authoritative {
		// Opening the chat reads complete history, which prefers a summary
		// record over every fallback below. A different list fallback must
		// not be written back over that title on each refresh.
		summary := strings.TrimSpace(latest(trecords, "summary", "summary"))
		authoritative = summary == "" || title == "" || summary == strings.TrimSpace(title)
	}
	if title == "" {
		title = latest(trecords, "", "summary")
	}
	if title == "" {
		title = prompt
	}
	if title == "" {
		title = media
	}
	if title == "" {
		if !options.IncludeUntitled {
			return LocalSession{}, false, true
		}
		authoritative = false
	}
	cwd := latest(trecords, "relocated", "relocatedCwd")
	if cwd == "" {
		cwd = claudeSampleString(head, "cwd", false)
	}
	if cwd == "" {
		cwd = candidate.project
	}
	if cwd == "" {
		// The head may hold only records without a cwd (compaction summaries,
		// snapshots or one huge record); conversation records at the end do.
		cwd = claudeSampleString(tail, "cwd", true)
	}
	if candidate.verifyProject && cwd != "" && !claudePathEqual(cwd, candidate.project) {
		return LocalSession{}, false, true
	}
	if candidate.project != "" && cwd != "" && !claudePathEqual(cwd, candidate.project) && claudePathEqual(claudeEncodedDirectory(cwd), claudeEncodedDirectory(candidate.project)) {
		// Lossy encoding can merge distinct directories (a-b and a/b). Compare
		// real paths before attributing such a file to the requested project.
		if real, err := filepath.EvalSymlinks(cwd); err == nil && !claudePathEqual(real, candidate.project) {
			return LocalSession{}, false, true
		}
	}
	stable = true
	// A continued-in record only supersedes the old session if no real message
	// follows it and its replacement transcript is available.
	for i := len(trecords) - 1; i >= 0; i-- {
		r := trecords[i]
		if claudeRealMessage(r) {
			break
		}
		if r["type"] == "continued-in" {
			stable = false
			id := stringValue(r["continuedInSessionId"])
			if _, err := uuid.Parse(id); err == nil && claudeHasConversation(filepath.Join(filepath.Dir(candidate.path), id+".jsonl")) {
				return LocalSession{}, false, false
			}
			break
		}
	}
	branch := claudeSampleString(tail, "gitBranch", true)
	if branch == "" {
		branch = claudeSampleString(head, "gitBranch", false)
	}
	created := nativeTimestamp(claudeSampleString(head, "timestamp", false))
	return LocalSession{ID: candidate.id, Path: candidate.path, Directory: cwd, Title: title,
		CustomTitle: custom, FirstPrompt: prompt, GitBranch: branch, Tag: latest(trecords, "tag", "tag"),
		FileSize: info.Size(), Backend: chatmodel.BackendClaude, CreatedAt: created, UpdatedAt: info.ModTime().UnixMilli(),
		TitleAuthoritative: authoritative}, true, stable
}

var (
	claudeListCommand = regexp.MustCompile(`<command-name>(.*?)</command-name>`)
	claudeListBash    = regexp.MustCompile(`<bash-input>(.*?)</bash-input>`)
	claudeListMeta    = regexp.MustCompile(`^(?:\s*<[a-z][\w-]*[\s>]|\[Request interrupted by user[^\]]*\])`)
)

func claudeListUserPrompt(record map[string]any, commandFallback *string) (prompt, media string) {
	if record["type"] != "user" || record["isMeta"] == true || record["isCompactSummary"] == true {
		return "", ""
	}
	message, _ := record["message"].(map[string]any)
	if blocks, ok := message["content"].([]any); ok {
		for _, block := range blocks {
			b, _ := block.(map[string]any)
			if b["type"] == "tool_result" {
				return "", ""
			}
			if b["type"] == "image" {
				media = "Image"
			}
			if b["type"] == "document" && media == "" {
				media = "Document"
			}
		}
	}
	texts, _ := userContentTexts(message["content"])
	for _, text := range texts {
		text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
		if match := claudeListCommand.FindStringSubmatch(text); len(match) > 1 {
			if *commandFallback == "" {
				*commandFallback = match[1]
			}
			continue
		}
		if match := claudeListBash.FindStringSubmatch(text); len(match) > 1 {
			return "! " + strings.TrimSpace(match[1]), media
		}
		if text == "" || claudeListMeta.MatchString(text) {
			continue
		}
		return claudePromptClip(text), media
	}
	return "", media
}

func claudePromptClip(text string) string {
	// JS limits this preview to 200 UTF-16 code units, without splitting a
	// surrogate pair. Preserve that limit for non-ASCII titles too.
	units := 0
	for i, r := range text {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if units+n > 200 {
			return strings.TrimSpace(text[:i]) + "…"
		}
		units += n
	}
	return text
}

func claudeRealMessage(record map[string]any) bool {
	if record["type"] == "assistant" {
		message, _ := record["message"].(map[string]any)
		_, stopped := message["stop_reason"].(string)
		return stopped && record["isApiErrorMessage"] != true
	}
	commandFallback := ""
	prompt, _ := claudeListUserPrompt(record, &commandFallback)
	return prompt != ""
}

func claudeHasConversation(path string) bool {
	head, tail, info, err := claudeSamples(path)
	if err != nil || info == nil {
		return false
	}
	if strings.Contains(head, `"parentUuid":`) || strings.Contains(tail, `"parentUuid":`) {
		return true
	}
	if info.Size() <= claudeSessionSampleBytes {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	// Match the SDK's bounded fallback for a very large first record.
	reader := io.LimitReader(file, 16*1024*1024)
	buf := make([]byte, 1024*1024)
	previous := ""
	for {
		n, err := reader.Read(buf)
		part := previous + string(buf[:n])
		if strings.Contains(part, `"parentUuid":`) {
			return true
		}
		if err == io.EOF {
			return info.Size() >= 16*1024*1024
		}
		if err != nil {
			return false
		}
		previous = part[max(0, len(part)-len(`"parentUuid":`)):]
	}
}
