package relay

import (
	_ "embed"
	"encoding/json"
)

//go:embed version.json
var versionMetadata []byte

// Version is the release version embedded in this Relay binary.
func Version() string {
	var metadata struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(versionMetadata, &metadata) != nil || metadata.Version == "" {
		return "development"
	}
	return metadata.Version
}
