package model

import "testing"

func TestCatalogCloneKeepsMaintainedDescriptionsIndependent(t *testing.T) {
	original := CodexCatalog{Models: []CodexModel{{Model: "one", Summary: &EntitySummary{Text: "Original", Version: "first"}}}}
	clone := original.Clone()
	clone.Models[0].Summary.Text = "Edited"
	if original.Models[0].Summary.Text != "Original" {
		t.Fatal("editing a cloned catalog mutated the original description")
	}
}
