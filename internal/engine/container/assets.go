// Package container runs a profile's agent inside a Docker container whose only route
// to the internet is a squid proxy enforcing a domain allowlist (the real network boundary).
package container

import "embed"

// GoldenBaseSourceImage is the digest-pinned upstream image used by the safeslop
// golden base Dockerfile. Keep it in sync with Dockerfile.agent so lock/profile
// dry-runs can report provenance without parsing Dockerfile text.
const GoldenBaseSourceImage = "debian:bookworm-slim@sha256:60eac759739651111db372c07be67863818726f754804b8707c90979bda511df"

//go:embed assets
var assetsFS embed.FS

// readAsset returns an embedded asset's bytes (path relative to assets/).
func readAsset(name string) ([]byte, error) { return assetsFS.ReadFile("assets/" + name) }
