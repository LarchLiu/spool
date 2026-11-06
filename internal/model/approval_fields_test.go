package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestApprovalAuditSummaryDescribesOnlyTheRequest(t *testing.T) {
	for _, test := range []struct {
		request ApprovalRequest
		want    string
	}{
		{ApprovalRequest{Title: "Computer Use", Reason: "Allow use of Magpie?", URL: "https://example.com/auth?token=secret"}, "Computer Use · Allow use of Magpie?"},
		{ApprovalRequest{Title: "Command execution", Reason: "Need network", Detail: "git push origin dev\ncwd: /work"}, "Command execution · git push origin dev"},
		{ApprovalRequest{Title: "Workspace file changes", Detail: "/work/project"}, "Workspace file changes · /work/project"},
		{ApprovalRequest{Title: "Additional permissions", Detail: "{\n  \"network\": true\n}"}, `Additional permissions · { "network": true }`},
		{ApprovalRequest{Title: " Tool\n", Reason: "  App\t access\x00needed  "}, "Tool · App access needed"},
		{ApprovalRequest{Title: "Tool", Reason: "Tool"}, "Tool"},
	} {
		if got := test.request.AuditSummary(); got != test.want {
			t.Fatalf("summary=%q want=%q", got, test.want)
		}
	}
	got := (ApprovalRequest{Title: "Computer Use", Reason: strings.Repeat("中文", 200)}).AuditSummary()
	if !utf8.ValidString(got) || len([]rune(got)) != 200 || !strings.HasSuffix(got, "…") {
		t.Fatal("audit summary must be bounded and preserve Unicode")
	}
}

func TestApprovalContentValidation(t *testing.T) {
	minimum, maximum := 1.0, 3.0
	minLength := 2
	request := ApprovalRequest{Fields: []ApprovalField{
		{Name: "confirm", Label: "Confirm", Type: "boolean", Required: true},
		{Name: "name", Label: "Name", Type: "string", Required: true, MinLength: &minLength},
		{Name: "count", Label: "Count", Type: "integer", Minimum: &minimum, Maximum: &maximum},
		{Name: "app", Label: "App", Type: "string", Options: []ApprovalOption{{Value: "calc"}, {Value: "notes"}}},
	}}
	for _, test := range []struct {
		name  string
		data  map[string]any
		valid bool
	}{
		{"false is a valid answer", map[string]any{"confirm": false, "name": "你好"}, true},
		{"bounded integer and choice", map[string]any{"confirm": true, "name": "Alex", "count": float64(3), "app": "calc"}, true},
		{"missing boolean", map[string]any{"name": "Alex"}, false},
		{"string boolean", map[string]any{"confirm": "true", "name": "Alex"}, false},
		{"short name", map[string]any{"confirm": true, "name": "你"}, false},
		{"fractional integer", map[string]any{"confirm": true, "name": "Alex", "count": 1.5}, false},
		{"outside bounds", map[string]any{"confirm": true, "name": "Alex", "count": float64(4)}, false},
		{"unadvertised option", map[string]any{"confirm": true, "name": "Alex", "app": "terminal"}, false},
		{"unexpected field", map[string]any{"confirm": true, "name": "Alex", "persist": "always"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := request.ValidateResponse(ApprovalResponse{Allow: true, Content: test.data}); (err == nil) != test.valid {
				t.Fatalf("validation=%v, want valid=%v", err, test.valid)
			}
		})
	}
	if err := request.ValidateResponse(ApprovalResponse{}); err != nil {
		t.Fatal("declining requires no form content")
	}
}

func TestApprovalArrayChoicesAndFormats(t *testing.T) {
	minItems := 1
	request := ApprovalRequest{Fields: []ApprovalField{{Name: "apps", Label: "Apps", Type: "array", Required: true, MinItems: &minItems,
		Options: []ApprovalOption{{Value: "calc"}, {Value: "notes"}}}}}
	for _, values := range [][]any{nil, {}, {"calc", "calc"}, {"terminal"}, {true}} {
		if err := request.ValidateResponse(ApprovalResponse{Allow: true, Content: map[string]any{"apps": values}}); err == nil {
			t.Fatalf("invalid array accepted: %#v", values)
		}
	}
	if err := request.ValidateResponse(ApprovalResponse{Allow: true, Content: map[string]any{"apps": []any{"calc", "notes"}}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ format, valid, invalid string }{
		{"email", "alex@example.com", "Alex <alex@example.com>"},
		{"uri", "https://example.com", "/relative"},
		{"date", "2026-11-06", "2026-02-30"},
		{"date-time", "2026-11-06T01:00:00Z", "2026-11-06"},
	} {
		if !validApprovalFormat(test.valid, test.format) || validApprovalFormat(test.invalid, test.format) {
			t.Fatalf("format %s not enforced", test.format)
		}
	}
}

func TestDecisionAuditPreservesScopeAndNeverSubmittedAnswers(t *testing.T) {
	request := ApprovalRequest{Title: "Command execution", Detail: strings.Repeat("command ", 100), Decisions: []ApprovalDecision{{ID: "session", Label: "Allow for this session", Kind: "approve"}}}
	kind, summary := request.ResponseAudit(ApprovalResponse{Decision: "session", Content: map[string]any{"secret": "sensitive-value"}})
	if kind != "approve" || !strings.HasSuffix(summary, " · Allow for this session") || len([]rune(summary)) > 200 || strings.Contains(summary, "sensitive-value") {
		t.Fatal(kind, summary)
	}
}
