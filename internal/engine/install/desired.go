package install

// DesiredState is the embedded, pinned + checksummed install manifest for darwin-arm64 — the only
// platform SafeSlop targets (specs/0012). It is the desired-state half of `install plan`; apply
// (SP7b-3) consumes URL + SHA256. Bump entries as data edits; TestDesiredStateIsFailClosed +
// ValidateDesired guarantee every entry stays fully pinned. Two tools probed by Status are absent
// here by design: nix is installer-managed via the *verified-installer* route instead (a pinned
// nix-installer binary in internal/engine/tools, since a single pinned binary can't express its
// multi-component system install); docker is genuinely unmanaged — on darwin the CLI is brew-only
// and the daemon is Docker Desktop / OrbStack (GUI casks), with no single-artifact verified
// installer to pin, so it stays a deliberate later slice.
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
			Name:       "mise",
			Kind:       "toolchain",
			Format:     FormatBinaryTarball,
			Version:    "2026.6.11",
			SHA256:     "084c352a9c5d1a19bd31fef84ba9692952aa04e8d2e3fe666948db35dedfaf95",
			URL:        "https://github.com/jdx/mise/releases/download/v2026.6.11/mise-v2026.6.11-macos-arm64.tar.gz",
			Provenance: ProvenanceVendor, // matches mise's published SHASUMS256.txt
		},
		{
			Name:       "tart",
			Kind:       "runtime",
			Format:     FormatAppTarball,
			Version:    "2.32.1",
			SHA256:     "8554ab4f7fc12afe52f9b7e3093a935673cbac737a83973d2db7a0683c814529",
			URL:        "https://github.com/cirruslabs/tart/releases/download/2.32.1/tart.tar.gz",
			Provenance: ProvenanceVendor, // matches tart's published checksums file
		},
		{
			// uv ships a versioned darwin-arm64 binary tarball + a per-artifact .sha256 (verified to
			// match this pin on 2026-06-21). Pinning the release here lets the cockpit install uv via the
			// fail-closed Route A (sha256 → notarized-binary trust chain) instead of `curl … | sh`
			// (specs/0036 item ①). No authoritative minisign pubkey is published (no .minisig asset), so
			// sha256 is the floor — same precedent as mise/tart above.
			Name:       "uv",
			Kind:       "toolchain",
			Format:     FormatBinaryTarball,
			Version:    "0.11.23",
			SHA256:     "71ef9de85db820749b3b12b7585624ee279e9c5afcbc6f8236bc3d628c4305b0",
			URL:        "https://github.com/astral-sh/uv/releases/download/0.11.23/uv-aarch64-apple-darwin.tar.gz",
			Provenance: ProvenanceVendor, // matches uv's published per-artifact .sha256
		},
		{
			// bun ships a darwin-arm64 .zip; sha256 verified to match bun's published SHASUMS256.txt on
			// 2026-06-21. Pinning it routes the cockpit install through verified Route A instead of
			// `curl -fsSL https://bun.sh/install | bash` (specs/0036 Task 5). The zip holds
			// bun-darwin-aarch64/bun; installBinary's findFile resolves it. No minisig published.
			Name:       "bun",
			Kind:       "runtime",
			Format:     FormatBinaryZip,
			Version:    "1.3.14",
			SHA256:     "d8b96221828ad6f97ac7ac0ab7e95872341af763001e8803e8267652c2652620",
			URL:        "https://github.com/oven-sh/bun/releases/download/bun-v1.3.14/bun-darwin-aarch64.zip",
			Provenance: ProvenanceVendor, // matches bun's published SHASUMS256.txt
		},
		{
			// pnpm ships a darwin-arm64 tar.gz whose root `pnpm` is a self-contained Node SEA binary
			// (the bundled dist/ is redundant at runtime). Pinning it replaces
			// `curl -fsSL https://get.pnpm.io/install.sh | sh -` with verified Route A (specs/0036 Task 5).
			// pnpm publishes NO checksum or signature asset, so this sha256 (computed from the GitHub
			// release over TLS on 2026-06-21) is the trust floor — still fail-closed on every install,
			// but with weaker pin-time provenance than uv/bun/mise; revisit if pnpm starts publishing sums.
			Name:       "pnpm",
			Kind:       "runtime",
			Format:     FormatBinaryTarball,
			Version:    "11.8.0",
			SHA256:     "7c9ef7523abf1190a2fde2b81dd652260d1679ba471c09950e8a08fa772c06e2",
			URL:        "https://github.com/pnpm/pnpm/releases/download/v11.8.0/pnpm-darwin-arm64.tar.gz",
			Provenance: ProvenanceTLS, // pnpm publishes no checksum asset — this sha is safeslop's TOFU hash
		},
		{
			// Claude Code ships a bare darwin-arm64 binary (no archive) at a versioned URL, with a
			// per-version manifest.json carrying a sha256 — verified to match this pin on 2026-06-22. The
			// pin replaces `curl -fsSL https://claude.ai/install.sh | bash` with verified Route A. The
			// binary self-updates after install, so a slightly-old pin only affects the bootstrap. Named
			// "claude" (the binary) so it matches both the Status probe and the "Claude Code" catalog
			// entry's Detect name.
			Name:       "claude",
			Kind:       "runtime",
			Format:     FormatRawBinary,
			Version:    "2.1.176",
			SHA256:     "f3f1b0c098510bd5d472b15f5541bb261f5939aeb52e488760bc53fb54c1803d",
			URL:        "https://downloads.claude.ai/claude-code-releases/2.1.176/darwin-arm64/claude",
			Provenance: ProvenanceVendor, // matches the per-version manifest.json sha on downloads.claude.ai
		},
	}
}
