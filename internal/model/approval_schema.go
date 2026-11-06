package model

import (
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type approvalSchemaLoader struct{}

func (approvalSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external form schema references are not supported")
}

// Compile only self-contained schemas. A form must never read a local file or
// fetch a URL simply because a provider put a $ref in an approval request.
func CompileApprovalSchema(value map[string]any) (*jsonschema.Schema, error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > 128*1024 {
		return nil, fmt.Errorf("form schema is too large")
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(approvalSchemaLoader{})
	const location = "https://spool.invalid/approval-schema.json"
	if err := c.AddResource(location, value); err != nil {
		return nil, err
	}
	return c.Compile(location)
}
