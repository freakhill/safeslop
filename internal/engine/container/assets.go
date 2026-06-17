// Package container runs a profile's agent inside a Docker container whose only route
// to the internet is a squid proxy enforcing a domain allowlist (the real network boundary).
package container

import "embed"

//go:embed assets
var assetsFS embed.FS

// readAsset returns an embedded asset's bytes (path relative to assets/).
func readAsset(name string) ([]byte, error) { return assetsFS.ReadFile("assets/" + name) }
