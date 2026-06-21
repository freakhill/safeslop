package install

// DesiredState is the embedded, pinned + checksummed install manifest for darwin-arm64 — the only
// platform SafeSlop targets (specs/0012). It is the desired-state half of `install plan`; apply
// (SP7b-3) consumes URL + SHA256. Bump entries as data edits; TestDesiredStateIsFailClosed +
// ValidateDesired guarantee every entry stays fully pinned. Tools probed by Status but absent here
// (docker, nix) are not yet installer-managed — their multi-component installers are a later slice.
//
// Checksums are read from each release's official checksum file (mise SHASUMS256.txt,
// tart_<ver>_checksums.txt). Bump version+sha256+url together when pinning a newer release.
//
// Upstream-signature pinning (Pin.Sig, specs/0012 §10.2) is built and tested (VerifyMinisign +
// the Apply sig-chain tests) but NOT yet activated for these tools: mise publishes
// SHASUMS256.txt.minisig but does not publish an authoritative minisign *public key* (its own
// installer leaves "verify with minisign or gpg" as a TODO), and tart's releases ship only a
// plain checksums file. Both therefore rely on the embedded-sha256 → notarized-binary trust chain
// (still fail-closed). Add a Sig here as a one-line data edit once an authoritative pubkey exists.
func DesiredState() []Pin {
	return []Pin{
		{
			Name:    "mise",
			Kind:    "toolchain",
			Format:  FormatBinaryTarball,
			Version: "2026.6.11",
			SHA256:  "084c352a9c5d1a19bd31fef84ba9692952aa04e8d2e3fe666948db35dedfaf95",
			URL:     "https://github.com/jdx/mise/releases/download/v2026.6.11/mise-v2026.6.11-macos-arm64.tar.gz",
		},
		{
			Name:    "tart",
			Kind:    "runtime",
			Format:  FormatAppTarball,
			Version: "2.32.1",
			SHA256:  "8554ab4f7fc12afe52f9b7e3093a935673cbac737a83973d2db7a0683c814529",
			URL:     "https://github.com/cirruslabs/tart/releases/download/2.32.1/tart.tar.gz",
		},
		{
			// uv ships a versioned darwin-arm64 binary tarball + a per-artifact .sha256 (verified to
			// match this pin on 2026-06-21). Pinning the release here lets the cockpit install uv via the
			// fail-closed Route A (sha256 → notarized-binary trust chain) instead of `curl … | sh`
			// (specs/0036 item ①). No authoritative minisign pubkey is published (no .minisig asset), so
			// sha256 is the floor — same precedent as mise/tart above.
			Name:    "uv",
			Kind:    "toolchain",
			Format:  FormatBinaryTarball,
			Version: "0.11.23",
			SHA256:  "71ef9de85db820749b3b12b7585624ee279e9c5afcbc6f8236bc3d628c4305b0",
			URL:     "https://github.com/astral-sh/uv/releases/download/0.11.23/uv-aarch64-apple-darwin.tar.gz",
		},
	}
}
