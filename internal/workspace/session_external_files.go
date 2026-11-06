package workspace

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

var codexFileCitation = regexp.MustCompile(`:codex-file-citation\{((?:[^"{}\r\n]|"(?:[^"\\\r\n]|\\.)*")*)\}`)
var codexCitationAttribute = regexp.MustCompile(`^[ \t]*([A-Za-z_][\w-]*)=("(?:[^"\\\r\n]|\\.)*")`)

// Citation paths are JSON strings, so escaped quotes and Unicode must resolve
// to the same individual file reference as ordinary Markdown links.
func codexCitationReferencesFile(text, path string) bool {
	for _, match := range codexFileCitation.FindAllStringSubmatch(text, -1) {
		attributes := match[1]
		values := map[string]string{}
		valid := true
		for strings.Trim(attributes, " \t") != "" {
			attribute := codexCitationAttribute.FindStringSubmatch(attributes)
			if attribute == nil {
				valid = false
				break
			}
			var value string
			_, duplicate := values[attribute[1]]
			if duplicate || json.Unmarshal([]byte(attribute[2]), &value) != nil {
				valid = false
				break
			}
			values[attribute[1]] = value
			attributes = attributes[len(attribute[0]):]
			if attributes != "" && attributes[0] != ' ' && attributes[0] != '\t' {
				valid = false
				break
			}
		}
		if valid && values["path"] == path {
			return true
		}
	}
	return false
}

func fileReferenceBoundary(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("\"'`<>[](){},;", r)
}

// Match complete path references, including Markdown, shell output, JSON tool
// arguments and line suffixes. A prefix such as /tmp/report must never grant
// access to /tmp/report-secret or to children of a referenced directory.
func textReferencesFile(text, path string) bool {
	variants := []string{path, filepath.ToSlash(path), (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()}
	for _, variant := range variants {
		remaining := text
		for variant != "" {
			i := strings.Index(remaining, variant)
			if i < 0 {
				break
			}
			before, after := remaining[:i], remaining[i+len(variant):]
			left := before == ""
			if !left {
				r, _ := utf8.DecodeLastRuneInString(before)
				left = fileReferenceBoundary(r) || strings.HasSuffix(before, "file://") || strings.HasSuffix(before, "file://localhost")
			}
			if left {
				// Native file links commonly append :line:column or #Lline.
				suffix := after
				if strings.HasPrefix(suffix, ":") || strings.HasPrefix(suffix, "#L") {
					prefix := 1
					if suffix[0] == '#' {
						prefix = 2
					}
					if len(suffix) > prefix && suffix[prefix] >= '0' && suffix[prefix] <= '9' {
						suffix = strings.TrimLeft(suffix[prefix:], "0123456789:C-L")
					}
				}
				if suffix == "" {
					return true
				}
				r, _ := utf8.DecodeRuneInString(suffix)
				if fileReferenceBoundary(r) || suffix == "." || strings.HasPrefix(suffix, ". ") || strings.HasPrefix(suffix, ".\n") {
					return true
				}
			}
			remaining = after
		}
	}
	return false
}

func sessionReferencesFile(chat chatmodel.Session, path string) bool {
	var reference func(any) bool
	reference = func(value any) bool {
		switch value := value.(type) {
		case string:
			return textReferencesFile(value, path)
		case []any:
			for _, item := range value {
				if reference(item) {
					return true
				}
			}
		case map[string]any:
			for _, item := range value {
				if reference(item) {
					return true
				}
			}
		}
		return false
	}
	for _, part := range chat.Transcript.Parts {
		if part.Role == "user" && part.Kind == "context" && part.FilePath == path && part.Name == filepath.Base(path) {
			return true
		}
		if part.Role != "assistant" && part.Role != "tool" {
			continue
		}
		if textReferencesFile(part.Text, path) || codexCitationReferencesFile(part.Text, path) {
			return true
		}
		var structured any
		if json.Unmarshal([]byte(part.Text), &structured) == nil && reference(structured) {
			return true
		}
	}
	return false
}

func sessionUserAttachment(chat chatmodel.Session, path string) bool {
	for _, part := range chat.Transcript.Parts {
		if part.Role == "user" && part.Kind == "context" && part.FilePath == path && part.Name == filepath.Base(path) {
			return true
		}
	}
	return false
}

// External access grants one referenced regular file, never its parent folder.
// An explicit user attachment grants that file independently of the project's
// agent-output switch. Chat membership is checked before reaching this route.
func (a *App) externalSessionFile(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, directory string) bool {
	path := r.URL.Query().Get("path")
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	snapshot := a.snapshot(chat)
	attached := sessionUserAttachment(snapshot, path)
	resolved := canonicalDirectory(path)
	if root := canonicalDirectory(directory); !attached && root != "" && resolved != "" && directoryWithin(resolved, root) {
		return false
	}
	project, err := a.store.Project(chat.ProjectID)
	if err != nil || (!attached && !project.AllowExternalFiles) {
		return false
	}
	if protectedProjectPath(path) || protectedProjectPath(resolved) || (!attached && !sessionReferencesFile(snapshot, path)) {
		projectFileError(w, os.ErrPermission)
		return true
	}
	if directoryWithin(resolved, a.store.Root()) {
		projectFileError(w, os.ErrPermission)
		return true
	}
	for _, variable := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if home := canonicalDirectory(os.Getenv(variable)); home != "" && directoryWithin(resolved, home) {
			projectFileError(w, os.ErrPermission)
			return true
		}
	}
	if chat.NativeSessionPath != "" && resolved == canonicalDirectory(chat.NativeSessionPath) {
		projectFileError(w, os.ErrPermission)
		return true
	}
	info, err := os.Lstat(path)
	if err != nil {
		projectFileError(w, err)
		return true
	}
	if !info.Mode().IsRegular() || resolved == "" {
		projectFileError(w, os.ErrPermission)
		return true
	}
	if !attached {
		projects, err := a.store.Projects()
		if err != nil {
			projectFileError(w, os.ErrPermission)
			return true
		}
		mappings := localSessionProjects(projects)
		match := matchLocalSessionProject(filepath.Dir(resolved), mappings)
		if match.ID != "" && match.ID != chat.ProjectID {
			projectFileError(w, os.ErrPermission)
			return true
		}
		_, common := gitWorkspace(filepath.Dir(resolved))
		for _, mapping := range mappings {
			// Agent references retain project boundaries; explicit attachments
			// grant only the individual file deliberately shared by the user.
			if mapping.project.ID != chat.ProjectID && match.ID == "" && (directoryWithin(resolved, mapping.path) || (common != "" && common == mapping.common && mapping.path == mapping.root)) {
				projectFileError(w, os.ErrPermission)
				return true
			}
		}
	}
	// A selected file can be readable while its parent directory cannot be
	// opened (for example, a macOS app container). Open only the authorized file
	// and verify its identity instead of requiring directory access.
	f, err := os.Open(resolved)
	if err != nil {
		projectFileError(w, err)
		return true
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		projectFileError(w, os.ErrPermission)
		return true
	}
	a.serveOpenFile(w, r, f, opened, filepath.ToSlash(path), true)
	return true
}
