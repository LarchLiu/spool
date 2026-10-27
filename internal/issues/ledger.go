package issues

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ledgerStart = "<!-- spool:issues:v1 -->\n```json\n"
const ledgerEnd = "\n```\n<!-- /spool:issues -->"

type Record struct {
	Number     int                        `json:"number"`
	Title      string                     `json:"title"`
	URL        string                     `json:"url"`
	Status     string                     `json:"status"`
	Categories []string                   `json:"categories"`
	Owner      Owner                      `json:"owner"`
	ClaimedAt  string                     `json:"claimed_at,omitempty"`
	Branch     string                     `json:"branch,omitempty"`
	Worktree   string                     `json:"worktree,omitempty"`
	PRs        []string                   `json:"prs,omitempty"`
	Validation string                     `json:"validation,omitempty"`
	Notes      string                     `json:"notes,omitempty"`
	History    []string                   `json:"history,omitempty"`
	Files      []string                   `json:"files,omitempty"`
	Extra      map[string]json.RawMessage `json:"-"`
}

type plainRecord Record

func (r *Record) UnmarshalJSON(body []byte) error {
	var p plainRecord
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	*r = Record(p)
	if err := json.Unmarshal(body, &r.Extra); err != nil {
		return err
	}
	known, _ := json.Marshal(p)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(known, &fields)
	for key := range fields {
		delete(r.Extra, key)
	}
	return nil
}
func (r Record) MarshalJSON() ([]byte, error) {
	body, err := json.Marshal(plainRecord(r))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	for key, value := range r.Extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

type Ledger struct {
	Path          string
	Records       map[int]*Record
	before, after string
	original      []byte
}

func (l *Ledger) SameRevision(other *Ledger) bool { return bytes.Equal(l.original, other.original) }

func Load(root string) (*Ledger, error) {
	l := &Ledger{Path: filepath.Join(root, "docs", "issues.md"), Records: map[int]*Record{}}
	if info, err := os.Lstat(l.Path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("ledger must be a regular file, not a symlink")
	}
	body, err := os.ReadFile(l.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l.original = body
	text := string(body)
	start := strings.Index(text, ledgerStart)
	if start < 0 {
		if strings.Contains(text, "spool:issues:") {
			return nil, fmt.Errorf("unrecognized ledger version/format; refusing to overwrite")
		}
		l.before = text
		if err := l.importTable(text); err != nil {
			return nil, err
		}
		return l, nil
	}
	tail := text[start+len(ledgerStart):]
	end := strings.Index(tail, ledgerEnd)
	if end < 0 || strings.Contains(tail[end+len(ledgerEnd):], "spool:issues:") {
		return nil, fmt.Errorf("malformed or duplicate ledger block")
	}
	var records []*Record
	if err = json.Unmarshal([]byte(tail[:end]), &records); err != nil || records == nil {
		return nil, fmt.Errorf("invalid ledger JSON; refusing to overwrite")
	}
	for _, record := range records {
		if record == nil || record.Number <= 0 || l.Records[record.Number] != nil || !validStatus(record.Status) {
			return nil, fmt.Errorf("invalid/duplicate issue record")
		}
		l.Records[record.Number] = record
	}
	l.before, l.after = text[:start], tail[end+len(ledgerEnd):]
	return l, nil
}

func validStatus(s string) bool { return s == "open" || s == "processing" || s == "pr" || s == "close" }

var issueNumber = regexp.MustCompile(`(?:^#?|/issues/|\[#)([0-9]+)`)

// Import common existing Markdown ledgers without removing their human text.
// Unrecognized in-progress claims fail closed instead of silently losing owners.
func (l *Ledger) importTable(text string) error {
	var header []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "|") {
			if strings.Contains(line, "processing") {
				return fmt.Errorf("existing processing record needs a structured ledger/table before automation can claim issues")
			}
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.Trim(strings.TrimSpace(cells[i]), "`")
		}
		isHeader := false
		for _, cell := range cells {
			if strings.EqualFold(cell, "status") || cell == "状态" {
				isHeader = true
			}
		}
		if isHeader {
			header = cells
			continue
		}
		values := map[string]string{}
		for i, cell := range cells {
			if i < len(header) {
				values[strings.ToLower(header[i])] = cell
			}
		}
		status := values["status"]
		if status == "" {
			status = values["状态"]
		}
		if !validStatus(status) {
			if strings.Contains(line, "processing") {
				return fmt.Errorf("cannot parse an existing processing claim")
			}
			continue
		}
		var number int
		for _, key := range []string{"issue", "number", "#", "编号", "issue 编号"} {
			if match := issueNumber.FindStringSubmatch(values[key]); len(match) > 1 {
				number, _ = strconv.Atoi(match[1])
				break
			}
		}
		if number <= 0 {
			return fmt.Errorf("cannot identify existing issue row: %s", line)
		}
		if l.Records[number] != nil {
			return fmt.Errorf("duplicate existing issue #%d", number)
		}
		r := &Record{Number: number, Status: status, Notes: line, Branch: values["branch"], Worktree: values["worktree"], Validation: values["validation"]}
		// Legacy owners are intentionally opaque: never steal a human claim.
		r.Owner.Automation = values["owner"]
		l.Records[number] = r
	}
	return nil
}

func (l *Ledger) Save() error {
	current, err := os.ReadFile(l.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !bytes.Equal(current, l.original) {
		return fmt.Errorf("shared ledger changed outside the project lock; refusing to overwrite")
	}
	records := make([]*Record, 0, len(l.Records))
	for _, record := range l.Records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Number < records[j].Number })
	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	prefix := l.before
	if prefix == "" {
		prefix = "# Issues automation\n\nLocal shared ledger. Do not commit this file to dev. Preserve owner/history when editing.\n\n"
	} else if !strings.HasSuffix(prefix, "\n\n") {
		prefix += "\n\n"
	}
	body = []byte(prefix + ledgerStart + string(body) + ledgerEnd + l.after)
	if bytes.Equal(body, l.original) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(l.Path), ".issues-ledger-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), l.Path); err != nil {
		return err
	}
	l.original = body
	return nil
}

func (l *Ledger) Sync(snapshot []Issue) {
	for _, issue := range snapshot {
		r := l.Records[issue.Number]
		if r == nil {
			r = &Record{Number: issue.Number, Status: "open"}
			l.Records[issue.Number] = r
		}
		previous := r.Status
		r.Title, r.URL = issue.Title, issue.URL
		// Existing claims keep their occupied categories even if labels change.
		categories := Categories(issue)
		if previous == "processing" {
			for _, c := range r.Categories {
				if !contains(categories, c) {
					categories = append(categories, c)
				}
			}
		}
		r.Categories = categories
		openPR := false
		for _, pr := range issue.PRs {
			if !contains(r.PRs, pr.URL) {
				r.PRs = append(r.PRs, pr.URL)
			}
			openPR = openPR || pr.Open()
		}
		switch {
		case issue.State == "closed":
			r.Status = "close"
		case openPR:
			r.Status = "pr"
		case previous == "processing" || (r.Owner.Run != "" && r.Branch != "" && r.Worktree != ""):
			r.Status = "processing"
		default:
			r.Status = "open"
		}
		if previous != r.Status {
			r.History = append(r.History, time.Now().UTC().Format(time.RFC3339)+" "+previous+" -> "+r.Status)
		}
	}
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Select resumes our oldest verifiably ended claim first. Unknown file scope
// on any other claim serializes all categories rather than risking overlap.
func (l *Ledger) Select(snapshot []Issue, ended func(Owner) bool) (*Issue, bool) {
	busy := map[string]int{}
	unknownScope := false
	for _, r := range l.Records {
		if r.Status == "processing" {
			for _, c := range r.Categories {
				busy[c]++
			}
			unknownScope = unknownScope || len(r.Files) == 0
		}
	}
	for _, issue := range snapshot {
		r := l.Records[issue.Number]
		if r.Status != "processing" || r.Owner.Automation != "issues" || !ended(r.Owner) || len(Categories(issue)) == 0 {
			continue
		}
		blocked := false
		for _, other := range l.Records {
			if other.Number == r.Number || other.Status != "processing" {
				continue
			}
			if len(other.Files) == 0 || len(r.Files) == 0 {
				blocked = true
			}
			for _, c := range r.Categories {
				if contains(other.Categories, c) {
					blocked = true
				}
			}
			for _, a := range r.Files {
				for _, b := range other.Files {
					if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
						blocked = true
					}
				}
			}
		}
		if !blocked {
			return &issue, true
		}
	}
	// A pending claim owned by this automation must be resolved before new work.
	for _, r := range l.Records {
		if r.Status == "processing" && r.Owner.Automation == "issues" {
			return nil, false
		}
	}
	if unknownScope {
		return nil, false
	}
	for _, issue := range snapshot {
		r := l.Records[issue.Number]
		if r.Status != "open" || len(Categories(issue)) == 0 {
			continue
		}
		blocked := false
		for _, c := range r.Categories {
			blocked = blocked || busy[c] > 0
		}
		// New issues have unknown file scope until investigated. Defer them
		// while any independent task is processing, even in another category.
		if len(busy) > 0 {
			blocked = true
		}
		if !blocked {
			return &issue, false
		}
	}
	return nil, false
}
