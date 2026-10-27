package model

import "fmt"

type CodexOptions struct {
	Model           string
	ReasoningEffort string
}

type ReasoningOption struct {
	Effort      string `json:"reasoningEffort"`
	Description string `json:"description"`
}

type CodexModel struct {
	Model         string            `json:"model"`
	DisplayName   string            `json:"displayName"`
	DefaultEffort string            `json:"defaultReasoningEffort"`
	Efforts       []ReasoningOption `json:"supportedReasoningEfforts"`
	Hidden        bool              `json:"hidden,omitempty"`
	IsDefault     bool              `json:"isDefault,omitempty"`
}

type CodexCatalog struct {
	Models        []CodexModel `json:"models"`
	DefaultModel  string       `json:"defaultModel"`
	DefaultEffort string       `json:"defaultEffort"`
}

func ValidateCodexOptions(options CodexOptions) error {
	if len(options.Model) > 200 {
		return fmt.Errorf("model ID must be at most 200 bytes")
	}
	for _, ch := range options.Model {
		if ch < '!' || ch > '~' {
			return fmt.Errorf("model ID must not contain whitespace or non-ASCII characters")
		}
	}
	if len(options.ReasoningEffort) > 32 {
		return fmt.Errorf("invalid reasoning level")
	}
	for _, ch := range options.ReasoningEffort {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return fmt.Errorf("invalid reasoning level")
		}
	}
	return nil
}

func (c CodexCatalog) Find(model string) (CodexModel, bool) {
	for _, item := range c.Models {
		if item.Model == model {
			return item, true
		}
	}
	return CodexModel{}, false
}

func (c CodexCatalog) Clone() CodexCatalog {
	c.Models = append([]CodexModel(nil), c.Models...)
	for i := range c.Models {
		c.Models[i].Efforts = append([]ReasoningOption(nil), c.Models[i].Efforts...)
	}
	return c
}

func (c CodexCatalog) Validate(options CodexOptions) error {
	if err := ValidateCodexOptions(options); err != nil {
		return err
	}
	if options.ReasoningEffort == "" {
		return nil
	}
	model := options.Model
	if model == "" {
		model = c.DefaultModel
	}
	if entry, known := c.Find(model); known {
		if len(entry.Efforts) == 0 && options.ReasoningEffort == entry.DefaultEffort {
			return nil
		}
		for _, effort := range entry.Efforts {
			if effort.Effort == options.ReasoningEffort {
				return nil
			}
		}
		return fmt.Errorf("model %s does not support reasoning level %s", model, options.ReasoningEffort)
	}
	// Custom provider model IDs may not be listed; let the CLI validate those.
	return nil
}

func ValidateClaudeEffort(effort string) error {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		return nil
	default:
		return fmt.Errorf("unsupported Claude reasoning level: %s", effort)
	}
}
