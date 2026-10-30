//go:build tools

package tools

// Keep the pinned binding tool in this module without linking its generator into the APK.
import _ "golang.org/x/mobile/bind"
