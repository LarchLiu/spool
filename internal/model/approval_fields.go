package model

import (
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"
)

type ApprovalOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// ApprovalField is the primitive form subset defined by MCP elicitation.
type ApprovalField struct {
	Name        string           `json:"name"`
	Label       string           `json:"label"`
	Description string           `json:"description,omitempty"`
	Type        string           `json:"type"`
	Format      string           `json:"format,omitempty"`
	Required    bool             `json:"required,omitempty"`
	Options     []ApprovalOption `json:"options,omitempty"`
	Default     any              `json:"default,omitempty"`
	Minimum     *float64         `json:"minimum,omitempty"`
	Maximum     *float64         `json:"maximum,omitempty"`
	MinLength   *int             `json:"min_length,omitempty"`
	MaxLength   *int             `json:"max_length,omitempty"`
	MinItems    *int             `json:"min_items,omitempty"`
	MaxItems    *int             `json:"max_items,omitempty"`
	AllowOther  bool             `json:"allow_other,omitempty"`
	Secret      bool             `json:"secret,omitempty"`
	Multiline   bool             `json:"multiline,omitempty"`
	Step        *float64         `json:"step,omitempty"`
}

// ValidateResponse uses the server's pending request, never a client-supplied
// schema. Invalid submissions leave the approval pending so the user can retry.
func (r ApprovalRequest) ValidateResponse(response ApprovalResponse) error {
	decision, err := r.Decision(response)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(response.Feedback) > 4096 || (response.Feedback != "" && !r.Feedback) {
		return fmt.Errorf("Invalid decision feedback")
	}
	if decision.Kind != "approve" && decision.Kind != "submit" {
		return nil
	}
	if r.Unavailable != "" {
		return fmt.Errorf("This request requires input that this client cannot provide")
	}
	if r.Schema != nil {
		schema, err := CompileApprovalSchema(r.Schema)
		if err != nil {
			return fmt.Errorf("This request contains an invalid form schema")
		}
		content := response.Content
		if content == nil {
			content = map[string]any{}
		}
		if err := schema.Validate(content); err != nil {
			return fmt.Errorf("Form values do not match the requested schema: %s", Clip(err.Error(), 2000))
		}
		return nil
	}
	for key := range response.Content {
		if !slices.ContainsFunc(r.Fields, func(f ApprovalField) bool { return f.Name == key }) {
			return fmt.Errorf("Unexpected approval field: %s", key)
		}
	}
	for _, field := range r.Fields {
		value, exists := response.Content[field.Name]
		if !exists && !field.Required {
			continue
		}
		if !exists || !field.valid(value) {
			return fmt.Errorf("Invalid approval field: %s", field.Label)
		}
	}
	return nil
}

func (f ApprovalField) valid(value any) bool {
	option := func(value string) bool {
		return len(f.Options) == 0 || f.AllowOther || slices.ContainsFunc(f.Options, func(o ApprovalOption) bool { return o.Value == value })
	}
	switch f.Type {
	case "string":
		v, ok := value.(string)
		length := utf8.RuneCountInString(v)
		return ok && option(v) && validApprovalFormat(v, f.Format) && (f.MinLength == nil || length >= *f.MinLength) && (f.MaxLength == nil || length <= *f.MaxLength)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		v, ok := value.(float64)
		base := 0.0
		if f.Minimum != nil {
			base = *f.Minimum
		}
		return ok && !math.IsNaN(v) && !math.IsInf(v, 0) && (f.Type != "integer" || math.Trunc(v) == v) &&
			(f.Minimum == nil || v >= *f.Minimum) && (f.Maximum == nil || v <= *f.Maximum) &&
			(f.Step == nil || (*f.Step > 0 && math.Abs((v-base) / *f.Step - math.Round((v-base) / *f.Step)) < 1e-8))
	case "json":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		values, ok := value.([]any)
		if !ok || (f.MinItems != nil && len(values) < *f.MinItems) || (f.MaxItems != nil && len(values) > *f.MaxItems) {
			return false
		}
		if len(f.Options) == 0 && !f.AllowOther && len(values) > 0 {
			return false
		}
		seen := map[string]bool{}
		for _, value := range values {
			v, ok := value.(string)
			if !ok || !option(v) || seen[v] {
				return false
			}
			seen[v] = true
		}
		return true
	}
	return false
}

func validApprovalFormat(value, format string) bool {
	switch format {
	case "email":
		address, err := mail.ParseAddress(value)
		return err == nil && address.Address == value
	case "uri":
		address, err := url.Parse(value)
		return err == nil && address.IsAbs()
	case "date":
		_, err := time.Parse("2006-01-02", value)
		return err == nil
	case "date-time":
		_, err := time.Parse(time.RFC3339, value)
		return err == nil
	}
	return true
}
