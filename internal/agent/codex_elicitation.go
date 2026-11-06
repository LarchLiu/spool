package agent

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	chatmodel "spool/internal/model"
)

func codexElicitation(params map[string]any) chatmodel.ApprovalRequest {
	request := chatmodel.ApprovalRequest{
		Title:  chatmodel.Clip(stringValue(params["serverName"]), 240),
		Reason: chatmodel.Clip(stringValue(params["message"]), 8192),
	}
	meta, _ := params["_meta"].(map[string]any)
	if name := stringValue(meta["connector_name"]); name != "" {
		request.Title = chatmodel.Clip(name, 240)
	}
	if request.Title == "" {
		request.Title = "Tool approval"
	}
	if subtitle := stringValue(meta["subtitle"]); subtitle != "" {
		request.Detail = chatmodel.Clip(subtitle, 4096)
	}
	if display, ok := meta["tool_params_display"].([]any); ok {
		for _, entry := range display {
			item, _ := entry.(map[string]any)
			label := stringValue(item["display_name"])
			if label == "" {
				label = stringValue(item["name"])
			}
			if value := stringValue(item["value"]); value != "" {
				request.Detail = chatmodel.AppendBounded(request.Detail, "\n"+label+": "+value, 8192)
			}
		}
	}
	request.Detail = strings.TrimSpace(request.Detail)
	switch stringValue(params["mode"]) {
	case "form", "openai/form", "openaiForm":
		fields, err := codexElicitationFields(params["requestedSchema"])
		if err != nil {
			request.Unavailable = "This request uses a form that Spool cannot display yet."
		} else {
			request.Fields = fields
		}
	case "url":
		address := stringValue(params["url"])
		parsed, err := url.Parse(address)
		if err != nil || len(address) > 8192 || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.ContainsAny(address, "\r\n\t") {
			request.Unavailable = "This request contains an unsupported authorization link."
		} else {
			request.URL = address
		}
	case "openai/userVerification":
		request.Title = chatmodel.Clip(stringValue(params["title"]), 240)
		request.Reason = chatmodel.Clip(stringValue(params["description"]), 8192)
		request.Unavailable = "This request requires device verification in the Codex app."
	default:
		request.Unavailable = "This request uses a form that Spool cannot display yet."
	}
	return request
}

func codexElicitationFields(value any) ([]chatmodel.ApprovalField, error) {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing elicitation schema")
	}
	if len(schema) == 0 {
		return nil, nil // OpenAI tool approval with no additional form input.
	}
	if !schemaKeys(schema, "type", "properties", "required", "additionalProperties", "$schema", "title", "description") || schema["type"] != "object" {
		return nil, fmt.Errorf("unsupported elicitation schema")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) > 32 {
		return nil, fmt.Errorf("invalid elicitation properties")
	}
	required := map[string]bool{}
	if names, ok := schema["required"].([]any); ok {
		for _, value := range names {
			name, ok := value.(string)
			if !ok || properties[name] == nil {
				return nil, fmt.Errorf("invalid required elicitation field")
			}
			required[name] = true
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]chatmodel.ApprovalField, 0, len(names))
	for _, name := range names {
		property, ok := properties[name].(map[string]any)
		if !ok || len(name) > 240 || !schemaKeys(property, "type", "title", "description", "default", "enum", "enumNames", "oneOf", "format", "minimum", "maximum", "minLength", "maxLength", "items", "minItems", "maxItems") {
			return nil, fmt.Errorf("unsupported elicitation field")
		}
		field := chatmodel.ApprovalField{
			Name: name, Label: chatmodel.Clip(stringValue(property["title"]), 240),
			Description: chatmodel.Clip(stringValue(property["description"]), 4096),
			Type:        stringValue(property["type"]), Required: required[name], Default: property["default"],
			Format: stringValue(property["format"]),
		}
		if field.Label == "" {
			field.Label = name
		}
		if field.Format != "" && field.Format != "email" && field.Format != "uri" && field.Format != "date" && field.Format != "date-time" {
			return nil, fmt.Errorf("unsupported elicitation format")
		}
		for key, target := range map[string]**float64{"minimum": &field.Minimum, "maximum": &field.Maximum} {
			if value, ok := property[key].(float64); ok {
				*target = &value
			}
		}
		for key, target := range map[string]**int{"minLength": &field.MinLength, "maxLength": &field.MaxLength, "minItems": &field.MinItems, "maxItems": &field.MaxItems} {
			if value, ok := property[key].(float64); ok {
				if value < 0 || value > 1024*1024 || value != float64(int(value)) {
					return nil, fmt.Errorf("invalid elicitation bound")
				}
				n := int(value)
				*target = &n
			}
		}
		options := property
		switch field.Type {
		case "string", "boolean", "integer", "number":
		case "array":
			options, ok = property["items"].(map[string]any)
			if !ok || !schemaKeys(options, "type", "enum", "anyOf") {
				return nil, fmt.Errorf("unsupported elicitation array")
			}
		default:
			return nil, fmt.Errorf("unsupported elicitation type")
		}
		if entries, ok := options["enum"].([]any); ok {
			labels, _ := property["enumNames"].([]any)
			for i, entry := range entries {
				value, ok := entry.(string)
				if !ok {
					return nil, fmt.Errorf("unsupported elicitation option")
				}
				label := value
				if i < len(labels) && stringValue(labels[i]) != "" {
					label = stringValue(labels[i])
				}
				field.Options = append(field.Options, chatmodel.ApprovalOption{Value: value, Label: chatmodel.Clip(label, 240)})
			}
		}
		for _, key := range []string{"oneOf", "anyOf"} {
			if entries, ok := options[key].([]any); ok {
				for _, entry := range entries {
					option, _ := entry.(map[string]any)
					value, ok := option["const"].(string)
					if !ok {
						return nil, fmt.Errorf("unsupported elicitation option")
					}
					label := stringValue(option["title"])
					if label == "" {
						label = value
					}
					field.Options = append(field.Options, chatmodel.ApprovalOption{Value: value, Label: chatmodel.Clip(label, 240)})
				}
			}
		}
		if len(field.Options) > 100 || (field.Type == "array" && len(field.Options) == 0) {
			return nil, fmt.Errorf("unsupported elicitation options")
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func schemaKeys(schema map[string]any, allowed ...string) bool {
	for key := range schema {
		found := false
		for _, candidate := range allowed {
			found = found || key == candidate
		}
		if !found {
			return false
		}
	}
	return true
}
