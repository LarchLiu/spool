// Package spool embeds the resources shared by the desktop and headless binaries.
package spool

import (
	"embed"
	"encoding/json"
	"io/fs"
)

//go:generate go run ./tools/branding.go

//go:embed all:frontend
var frontend embed.FS

//go:embed locales/*.json
var locales embed.FS

//go:embed tray.png
var trayIcon []byte

//go:embed assets/icon.png
var appIcon []byte

//go:embed mygo.json
var appMetadata []byte

func Frontend() fs.FS        { return frontend }
func Locales() fs.ReadFileFS { return locales }
func TrayIcon() []byte       { return append([]byte(nil), trayIcon...) }
func AppIcon() []byte        { return append([]byte(nil), appIcon...) }

// Version supplies build metadata when running outside a packaged app bundle.
func Version() string {
	var metadata struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(appMetadata, &metadata) != nil || metadata.Version == "" {
		return "development"
	}
	return metadata.Version
}
