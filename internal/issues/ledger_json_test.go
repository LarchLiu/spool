package issues

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeLedgerFixture(t *testing.T, root, name, text string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJSONLedgerUsesVersionedDocumentAndPreservesUnknownFields(t *testing.T) {
	root := t.TempDir()
	path := writeLedgerFixture(t, root, "issues.json", `{
		"version": 1,
		"records": [{"number": 2, "status": "processing", "owner": {"automation": "human"}, "notes": "keep", "custom": {"keep": true}}],
		"custom_root": {"keep": [1, 2]}
	}`)
	// JSON is authoritative, even when the obsolete Markdown cannot be parsed.
	legacyPath := writeLedgerFixture(t, root, "issues.md", "issue #3 processing owned by somebody")
	l, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != path || len(l.Records) != 1 || l.Records[2].Owner.Automation != "human" {
		t.Fatalf("unexpected ledger: %#v", l)
	}
	l.Records[1] = &Record{Number: 1, Status: "open"}
	if err = l.Save(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document ledgerDocument
	if err = json.Unmarshal(body, &document); err != nil || document.Version != 1 || len(document.Records) != 2 || document.Records[0].Number != 1 {
		t.Fatalf("not a sorted, versioned JSON document: %s, %v", body, err)
	}
	l, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var customRoot struct {
		Keep []int `json:"keep"`
	}
	var customRecord struct {
		Keep bool `json:"keep"`
	}
	if err = json.Unmarshal(l.extra["custom_root"], &customRoot); err != nil || len(customRoot.Keep) != 2 {
		t.Fatal("lost unknown document field")
	}
	if err = json.Unmarshal(l.Records[2].Extra["custom"], &customRecord); err != nil || !customRecord.Keep || l.Records[2].Notes != "keep" {
		t.Fatal("lost unknown record field or notes")
	}
	if err = l.Save(); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(body, unchanged) {
		t.Fatal("unchanged save rewrote JSON")
	}
	legacy, _ := os.ReadFile(legacyPath)
	if string(legacy) != "issue #3 processing owned by somebody" {
		t.Fatal("JSON save touched obsolete Markdown")
	}
}

func TestJSONLedgerRejectsInvalidDocumentsWithoutMarkdownFallback(t *testing.T) {
	for _, text := range []string{
		"", "null", "[]", "{}", "not JSON", `{"version":1,"records":[]`,
		`{"version":2,"records":[]}`, `{"version":1}`, `{"version":1,"records":null}`,
		`{"version":1,"records":[null]}`, `{"version":1,"records":[{"number":0,"status":"open"}]}`,
		`{"version":1,"records":[{"number":1,"status":"invalid"}]}`,
		`{"version":1,"records":[{"number":1,"status":"open"},{"number":1,"status":"close"}]}`,
	} {
		t.Run(text, func(t *testing.T) {
			root := t.TempDir()
			path := writeLedgerFixture(t, root, "issues.json", text)
			writeLedgerFixture(t, root, "issues.md", "| Issue | Status |\n|---|---|\n| #1 | open |\n")
			if _, err := Load(root); err == nil {
				t.Fatalf("accepted invalid JSON ledger %q", text)
			}
			body, _ := os.ReadFile(path)
			if string(body) != text {
				t.Fatal("failed load overwrote JSON")
			}
		})
	}
}

func TestJSONLedgerCreatesEmptyRecordsArray(t *testing.T) {
	root := t.TempDir()
	l, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Save(); err != nil {
		t.Fatal(err)
	}
	l, err = Load(root)
	if err != nil || len(l.Records) != 0 {
		t.Fatalf("cannot reload empty ledger: %#v, %v", l, err)
	}
	if _, err = os.Stat(filepath.Join(root, "issues.md")); !os.IsNotExist(err) {
		t.Fatal("new ledger created a Markdown file")
	}
}

func TestJSONLedgerIgnoresMarkdownWithoutImportingItsContent(t *testing.T) {
	root := t.TempDir()
	text := "issue #3 processing owned by somebody\n<!-- spool:issues:v2 -->"
	path := writeLedgerFixture(t, root, "issues.md", text)
	l, err := Load(root)
	if err != nil || len(l.Records) != 0 {
		t.Fatalf("Markdown affected JSON ledger: %#v, %v", l, err)
	}
	writeLedgerFixture(t, root, "issues.md", text+"\nConcurrent Markdown edit")
	fresh, err := Load(root)
	if err != nil || !l.SameRevision(fresh) {
		t.Fatal("Markdown edit changed the JSON revision")
	}
	if err = l.Save(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(l.Path)
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil || len(fields) != 2 {
		t.Fatalf("new JSON includes Markdown content: %s, %v", body, err)
	}
	legacy, _ := os.ReadFile(path)
	if string(legacy) != text+"\nConcurrent Markdown edit" {
		t.Fatal("JSON save touched Markdown")
	}
}

func TestJSONLedgerDetectsConcurrentCreation(t *testing.T) {
	for _, text := range []string{`{"version":1,"records":[]}`, ""} {
		t.Run(text, func(t *testing.T) {
			root := t.TempDir()
			l, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			path := writeLedgerFixture(t, root, "issues.json", text)
			if fresh, err := Load(root); err == nil && l.SameRevision(fresh) {
				t.Fatal("revision ignored a concurrently created JSON file")
			}
			if err = l.Save(); err == nil {
				t.Fatal("save ignored a concurrently created JSON file")
			}
			body, _ := os.ReadFile(path)
			if string(body) != text {
				t.Fatal("save overwrote a concurrently created JSON file")
			}
		})
	}
}

func TestJSONLedgerRejectsSymlinksOnLoadAndSave(t *testing.T) {
	root := t.TempDir()
	target := writeLedgerFixture(t, root, "target.json", `{"version":1,"records":[]}`)
	path := filepath.Join(root, "issues.json")
	l, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err = Load(root); err == nil {
		t.Fatal("load followed ledger symlink")
	}
	if err = l.Save(); err == nil {
		t.Fatal("save followed ledger symlink")
	}
}

func TestJSONLedgerRefusesInvalidRecordsOnSave(t *testing.T) {
	for name, records := range map[string]map[int]*Record{
		"nil":            {1: nil},
		"invalid number": {0: {Number: 0, Status: "open"}},
		"mismatched key": {1: {Number: 2, Status: "open"}},
		"invalid status": {1: {Number: 1, Status: "invalid"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			original := `{"version":1,"records":[]}`
			path := writeLedgerFixture(t, root, "issues.json", original)
			l, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			l.Records = records
			if err = l.Save(); err == nil {
				t.Fatal("saved an invalid record")
			}
			body, _ := os.ReadFile(path)
			if string(body) != original {
				t.Fatal("invalid save changed the JSON file")
			}
		})
	}
}
