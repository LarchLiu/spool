package issues

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const ledgerVersion = 1

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
	Path     string
	Records  map[int]*Record
	extra    map[string]json.RawMessage
	original []byte
}

type ledgerDocument struct {
	Version int       `json:"version"`
	Records []*Record `json:"records"`
}

func (l *Ledger) SameRevision(other *Ledger) bool {
	return l.Path == other.Path && sameLedgerContents(l.original, other.original)
}

func sameLedgerContents(a, b []byte) bool {
	return (a == nil) == (b == nil) && bytes.Equal(a, b)
}

func readLedgerFile(path string) ([]byte, error) {
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ledger %s must be a regular file, not a symlink", path)
	}
	return os.ReadFile(path)
}

func Load(directory string) (*Ledger, error) {
	l := &Ledger{Path: filepath.Join(directory, "issues.json"), Records: map[int]*Record{}}
	body, err := readLedgerFile(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	} else if err != nil {
		return nil, fmt.Errorf("read issues ledger: %w", err)
	}
	l.original = body
	var document ledgerDocument
	if err = json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("invalid issues ledger JSON in %s; refusing to overwrite: %w", l.Path, err)
	}
	if document.Version != ledgerVersion || document.Records == nil {
		return nil, fmt.Errorf("unrecognized issues ledger version/format in %s; refusing to overwrite", l.Path)
	}
	if err = l.importRecords(document.Records); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(body, &l.extra); err != nil {
		return nil, err
	}
	for _, key := range []string{"version", "records"} {
		delete(l.extra, key)
	}
	return l, nil
}

func (l *Ledger) importRecords(records []*Record) error {
	for _, record := range records {
		if record == nil || record.Number <= 0 || l.Records[record.Number] != nil || !validStatus(record.Status) {
			return fmt.Errorf("invalid/duplicate issue record")
		}
		l.Records[record.Number] = record
	}
	return nil
}

func validStatus(s string) bool { return s == "open" || s == "processing" || s == "pr" || s == "close" }

func (l *Ledger) Save() error {
	current, err := readLedgerFile(l.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !sameLedgerContents(current, l.original) {
		return fmt.Errorf("shared ledger changed outside the project lock; refusing to overwrite")
	}
	records := make([]*Record, 0, len(l.Records))
	for number, record := range l.Records {
		if record == nil || number <= 0 || record.Number != number || !validStatus(record.Status) {
			return fmt.Errorf("invalid issue record #%d; refusing to save", number)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Number < records[j].Number })
	body, err := json.Marshal(ledgerDocument{Version: ledgerVersion, Records: records})
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil {
		return err
	}
	for key, value := range l.extra {
		fields[key] = value
	}
	if body, err = json.MarshalIndent(fields, "", "  "); err != nil {
		return err
	}
	body = append(body, '\n')
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

func (l *Ledger) Sync(snapshot []Issue, labels []string) {
	for _, issue := range snapshot {
		r := l.Records[issue.Number]
		if r == nil {
			r = &Record{Number: issue.Number, Status: "open"}
			l.Records[issue.Number] = r
		}
		previous := r.Status
		r.Title, r.URL = issue.Title, issue.URL
		// Existing claims keep their occupied categories even if labels change.
		categories := Categories(issue, labels)
		if previous == "processing" || r.Owner.Run != "" {
			if len(r.Categories) == 0 {
				// An unfiltered run keeps its global slot across settings changes.
				categories = []string{}
			} else {
				for _, c := range r.Categories {
					if !contains(categories, c) {
						categories = append(categories, c)
					}
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

// Empty categories reserve the whole project. Live executors keep reservations
// even after GitHub reports a PR or closed issue; elapsed time never frees them.
func (l *Ledger) CanClaim(record *Record, ended func(Owner) bool) bool {
	resuming := record.Status == "processing" && record.Owner.Automation == "issues" && ended(record.Owner)
	for _, other := range l.Records {
		if other.Number == record.Number {
			continue
		}
		live := other.Owner.Run != "" && !ended(other.Owner)
		if other.Status != "processing" && !live {
			continue
		}
		// Ended automated claims reserve priority over new work, not over each
		// other's recovery (e.g. when several label slots become one global slot).
		if !live && other.Owner.Automation == "issues" && ended(other.Owner) {
			if resuming || (len(other.Categories) == 0 && len(record.Categories) > 0) {
				// An ended global executor cannot block newly configured labels.
				continue
			}
		}
		if len(record.Categories) == 0 || len(other.Categories) == 0 {
			return false
		}
		for _, label := range record.Categories {
			if contains(other.Categories, label) {
				return false
			}
		}
	}
	return true
}

func (l *Ledger) Select(snapshot []Issue, labels []string, ended func(Owner) bool) (*Issue, bool) {
	for _, issue := range snapshot {
		r := l.Records[issue.Number]
		if r == nil || r.Status != "processing" || r.Owner.Automation != "issues" || !ended(r.Owner) || (len(labels) > 0 && len(Categories(issue, labels)) == 0) {
			continue
		}
		candidate := *r
		if len(labels) == 0 || len(candidate.Categories) == 0 {
			candidate.Categories = Categories(issue, labels)
		}
		if l.CanClaim(&candidate, ended) {
			return &issue, true
		}
	}
	for _, issue := range snapshot {
		r := l.Records[issue.Number]
		if r == nil || r.Status != "open" || (len(labels) > 0 && len(Categories(issue, labels)) == 0) || (r.Owner.Run != "" && !ended(r.Owner)) {
			continue
		}
		candidate := *r
		candidate.Categories = Categories(issue, labels)
		if l.CanClaim(&candidate, ended) {
			return &issue, false
		}
	}
	return nil, false
}
