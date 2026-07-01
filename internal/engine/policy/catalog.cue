package safeslop

// The package catalog (specs/0058 N0; storage migrated to CUE in specs/0059 W2). This
// is the safeslop-owned, curated source of truth — rendered to catalog.json and
// embedded into the binary via go:embed. A profile declares which build-time packages
// go into its container image by referencing catalog entries by name — individually
// (`packages`) or via named sets (`bundles`). Extending it is a code edit + review, and
// that review IS the supply-chain boundary — distinct from squid, the runtime network
// boundary (specs/0058 N2). Every entry is version-pinned; `binary` kinds also pin a
// per-arch content digest. This is the curated generalization of the old hardcoded
// ENABLE_CLAUDE_CODE/PI build args (identity.go). The package-version selection & bump
// policy that governs these pins is canonized in specs/research/2026-06-30-version-
// policy-flo.md; `safeslop catalog {bump,propose-version,add,audit}` (0059) enforces it.

catalog: {
	bundles: [{
		name:        "base-tools"
		description: "everyday CLI ergonomics: ripgrep, fd, bat, eza, fzf, zoxide"
		packages: ["ripgrep", "fd", "bat", "eza", "fzf", "zoxide"]
	}, {
		name:        "claude"
		description: "Claude Code (Anthropic) + Node runtime"
		packages: ["node", "claude-code"]
	}, {
		name:        "go"
		description: "Go toolchain (compiler + module proxy egress)"
		packages: ["go"]
	}, {
		name:        "node"
		description: "Node.js + pnpm + bun for JS/TS work"
		packages: ["node", "pnpm", "bun"]
	}, {
		name:        "personal"
		description: "daily-driver multi-language set: CLI ergonomics + Node/Python/Go/Rust toolchains"
		packages: ["ripgrep", "fd", "bat", "eza", "fzf", "zoxide", "yq", "node", "pnpm", "python3", "uv", "ruff", "go", "rust", "hyperfine", "tokei", "sccache"]
	}, {
		name:        "pi"
		description: "pi coding agent + Node runtime"
		packages: ["node", "pi"]
	}, {
		name:        "python"
		description: "Python 3 + uv + ruff (linter/formatter)"
		packages: ["python3", "uv", "ruff"]
	}, {
		name:        "rust"
		description: "Rust toolchain + common cargo subcommands"
		packages: ["rust", "cargo-nextest", "cargo-audit", "cargo-deny", "cargo-expand", "cargo-make", "cargo-watch", "sccache"]
	}, {
		name:        "rust-embedded"
		description: "Rust for no_std / embedded targets (cargo-binutils, flip-link)"
		packages: ["rust", "cargo-binutils", "flip-link"]
	}, {
		name:        "web"
		description: "JS/TS web development: TypeScript, Vite, ESLint, Prettier, web-ext"
		packages: ["node", "pnpm", "typescript", "vite", "eslint", "prettier", "web-ext"]
	}]
	// agentDefaultBundle: maps an agent to the bundle implied by selecting it, so
	// `--agent claude` installs claude-code without the user restating it. Agents absent
	// here (fish, zsh, shell) imply no packages — a tiny shell-only image. The default is
	// additive (always included so the agent can launch); an opt-out lands with the CLI.
	defaults: {
		claude: "claude"
		pi:     "pi"
	}
	packages: [{
		name:    "bat"
		kind:    "binary"
		version: "0.25.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/sharkdp/bat/releases"
			asset: {
				amd64: "https://github.com/sharkdp/bat/releases/download/v{version}/bat-v{version}-x86_64-unknown-linux-gnu.tar.gz"
				arm64: "https://github.com/sharkdp/bat/releases/download/v{version}/bat-v{version}-aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "bun"
		kind:    "binary"
		version: "1.1.38"
		note:    "provides bunx"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/oven-sh/bun/releases"
			asset: {
				amd64: "https://github.com/oven-sh/bun/releases/download/bun-v{version}/bun-linux-x64.zip"
				arm64: "https://github.com/oven-sh/bun/releases/download/bun-v{version}/bun-linux-aarch64.zip"
			}
		}
	}, {
		name:    "cargo-audit"
		kind:    "binary"
		version: "0.21.2"
		note:    "cargo-* subcommands all require the rust toolchain; their closure pulls rust in"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/rustsec/rustsec/releases"
		}
	}, {
		name:    "cargo-binutils"
		kind:    "binary"
		version: "0.3.6"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/rust-embedded/cargo-binutils/releases"
		}
	}, {
		name:    "cargo-deny"
		kind:    "binary"
		version: "0.18.2"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/EmbarkStudios/cargo-deny/releases"
		}
	}, {
		name:    "cargo-expand"
		kind:    "binary"
		version: "1.0.110"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/dtolnay/cargo-expand/releases"
		}
	}, {
		name:    "cargo-make"
		kind:    "binary"
		version: "0.37.26"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/sagiegurari/cargo-make/releases"
		}
	}, {
		name:    "cargo-nextest"
		kind:    "binary"
		version: "0.9.98"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/nextest-rs/nextest/releases"
		}
	}, {
		name:    "cargo-watch"
		kind:    "binary"
		version: "8.5.4"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/watchexec/cargo-watch/releases"
		}
	}, {
		name:    "claude-code"
		kind:    "npm"
		version: "2.1.121"
		note:    "runtime egress scoped to Anthropic API"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		runtimeEgress: [".anthropic.com"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/@anthropic-ai%2fclaude-code"
		}
	}, {
		name:    "eslint"
		kind:    "npm"
		version: "9.22.0"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/eslint"
		}
	}, {
		name:    "eza"
		kind:    "binary"
		version: "0.21.1"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/eza-community/eza/releases"
			asset: {
				amd64: "https://github.com/eza-community/eza/releases/download/v{version}/eza_x86_64-unknown-linux-gnu.tar.gz"
				arm64: "https://github.com/eza-community/eza/releases/download/v{version}/eza_aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "fd"
		kind:    "binary"
		version: "10.2.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/sharkdp/fd/releases"
			asset: {
				amd64: "https://github.com/sharkdp/fd/releases/download/v{version}/fd-v{version}-x86_64-unknown-linux-gnu.tar.gz"
				arm64: "https://github.com/sharkdp/fd/releases/download/v{version}/fd-v{version}-aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "flip-link"
		kind:    "binary"
		version: "0.1.12"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		requires: ["rust"]
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/knurling-rs/flip-link/releases"
		}
	}, {
		name:    "fzf"
		kind:    "binary"
		version: "0.59.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/junegunn/fzf/releases"
			asset: {
				amd64: "https://github.com/junegunn/fzf/releases/download/v{version}/fzf-{version}-linux_amd64.tar.gz"
				arm64: "https://github.com/junegunn/fzf/releases/download/v{version}/fzf-{version}-linux_arm64.tar.gz"
			}
		}
	}, {
		name:    "go"
		kind:    "binary"
		version: "1.26.0"
		note:    "the Go toolchain; go get/go build fetch modules via the module proxy + checksum DB (scoped exact hosts)"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["go.dev"]
		runtimeEgress: ["proxy.golang.org", "sum.golang.org"]
		upstream: {
			kind:        "url-regex"
			url:         "https://go.dev/dl/?mode=json&include=all"
			manifestURL: "https://go.dev/dl/?mode=json&include=all"
			asset: {
				amd64: "https://go.dev/dl/go{version}.linux-amd64.tar.gz"
				arm64: "https://go.dev/dl/go{version}.linux-arm64.tar.gz"
			}
		}
	}, {
		name:    "hyperfine"
		kind:    "binary"
		version: "1.19.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/sharkdp/hyperfine/releases"
			asset: {
				amd64: "https://github.com/sharkdp/hyperfine/releases/download/v{version}/hyperfine-v{version}-x86_64-unknown-linux-gnu.tar.gz"
				arm64: "https://github.com/sharkdp/hyperfine/releases/download/v{version}/hyperfine-v{version}-aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "mise"
		kind:    "binary"
		version: "2026.6.11"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/jdx/mise/releases"
			asset: {
				amd64: "https://github.com/jdx/mise/releases/download/v{version}/mise-v{version}-linux-x64.tar.gz"
				arm64: "https://github.com/jdx/mise/releases/download/v{version}/mise-v{version}-linux-arm64.tar.gz"
			}
		}
	}, {
		name:    "node"
		kind:    "binary"
		version: "22.23.1"
		note:    "official multi-arch tarball, sha256-verified per arch (digests from nodejs.org/dist/vX/SHASUMS256.txt; amd64 == the x64 tarball). Provides npm, which claude-code/pi/pnpm and the JS/TS dev tools require"
		sha256: {
			amd64: "9749e988f437343b7fa832c69ded82a312e41a03116d766797ac14f6f9eee578"
			arm64: "0294e8b915ab75f92c7513d2fcb830ae06e10684e6c603e99a87dbf8835389c1"
		}
		buildFetch: ["nodejs.org"]
		upstream: {
			kind:        "node-dist"
			url:         "https://nodejs.org/dist/index.json"
			manifestURL: "https://nodejs.org/dist/v{version}/SHASUMS256.txt"
			asset: {
				amd64: "https://nodejs.org/dist/v{version}/node-v{version}-linux-x64.tar.xz"
				arm64: "https://nodejs.org/dist/v{version}/node-v{version}-linux-arm64.tar.xz"
			}
		}
	}, {
		name:    "pi"
		kind:    "npm"
		version: "0.80.2"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/@earendil-works%2fpi-coding-agent"
		}
	}, {
		name:    "pnpm"
		kind:    "npm"
		version: "9.15.0"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/pnpm"
		}
	}, {
		name:    "prettier"
		kind:    "npm"
		version: "3.5.3"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/prettier"
		}
	}, {
		name:    "python3"
		kind:    "apt"
		version: "3.11"
		note:    "apt kind — transitives are frozen by the golden-base Debian-snapshot pin (specs/0058 N2.2); reserved for what only apt provides"
		buildFetch: ["deb.debian.org", "snapshot.debian.org"]
		upstream: {
			kind: "debian-snapshot"
			url:  "https://snapshot.debian.org/mr/package/python3.11/"
		}
	}, {
		name:    "ripgrep"
		kind:    "binary"
		version: "14.1.1"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/BurntSushi/ripgrep/releases"
			asset: {
				amd64: "https://github.com/BurntSushi/ripgrep/releases/download/{version}/ripgrep-{version}-x86_64-unknown-linux-musl.tar.gz"
				arm64: "https://github.com/BurntSushi/ripgrep/releases/download/{version}/ripgrep-{version}-aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "ruff"
		kind:    "binary"
		version: "0.11.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["astral.sh", "github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/astral-sh/ruff/releases"
			asset: {
				amd64: "https://github.com/astral-sh/ruff/releases/download/{version}/ruff-x86_64-unknown-linux-gnu.tar.gz"
				arm64: "https://github.com/astral-sh/ruff/releases/download/{version}/ruff-aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "rust"
		kind:    "binary"
		version: "1.96.0"
		note:    "the rustc+cargo toolchain tarball; cargo fetches crates + the sparse index from *.crates.io and toolchain artifacts from static.rust-lang.org (leading dot covers index/static)"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["static.rust-lang.org"]
		runtimeEgress: [".crates.io", "static.rust-lang.org"]
		upstream: {
			kind:        "url-regex"
			url:         "https://static.rust-lang.org/dist/channel-rust-stable.toml"
			manifestURL: "https://static.rust-lang.org/dist/channel-rust-stable.toml"
			asset: {
				amd64: "https://static.rust-lang.org/dist/rust-{version}-x86_64-unknown-linux-gnu.tar.xz"
				arm64: "https://static.rust-lang.org/dist/rust-{version}-aarch64-unknown-linux-gnu.tar.xz"
			}
		}
	}, {
		name:    "rustup"
		kind:    "binary"
		version: "1.28.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["static.rust-lang.org"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/rust-lang/rustup/releases"
			asset: {
				amd64: "https://static.rust-lang.org/rustup/archive/{version}/x86_64-unknown-linux-gnu/rustup-init"
				arm64: "https://static.rust-lang.org/rustup/archive/{version}/aarch64-unknown-linux-gnu/rustup-init"
			}
		}
	}, {
		name:    "sccache"
		kind:    "binary"
		version: "0.10.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/mozilla/sccache/releases"
		}
	}, {
		name:    "tokei"
		kind:    "binary"
		version: "13.0.0"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/XAMPPRocky/tokei/releases"
		}
	}, {
		name:    "typescript"
		kind:    "npm"
		version: "5.8.2"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/typescript"
		}
	}, {
		name:    "uv"
		kind:    "binary"
		version: "0.5.11"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["astral.sh", "github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/astral-sh/uv/releases"
			asset: {
				amd64: "https://github.com/astral-sh/uv/releases/download/{version}/uv-x86_64-unknown-linux-gnu.tar.gz"
				arm64: "https://github.com/astral-sh/uv/releases/download/{version}/uv-aarch64-unknown-linux-gnu.tar.gz"
			}
		}
	}, {
		name:    "vite"
		kind:    "npm"
		version: "6.3.5"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/vite"
		}
	}, {
		name:    "web-ext"
		kind:    "npm"
		version: "8.5.0"
		requires: ["node"]
		buildFetch: ["registry.npmjs.org"]
		upstream: {
			kind: "npm-registry"
			url:  "https://registry.npmjs.org/web-ext"
		}
	}, {
		name:    "yq"
		kind:    "binary"
		version: "4.45.4"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/mikefarah/yq/releases"
			asset: {
				amd64: "https://github.com/mikefarah/yq/releases/download/v{version}/yq_linux_amd64"
				arm64: "https://github.com/mikefarah/yq/releases/download/v{version}/yq_linux_arm64"
			}
		}
	}, {
		name:    "zoxide"
		kind:    "binary"
		version: "0.9.8"
		sha256: {
			amd64: "0000000000000000000000000000000000000000000000000000000000000000"
			arm64: "0000000000000000000000000000000000000000000000000000000000000000"
		}
		buildFetch: ["github.com"]
		upstream: {
			kind: "github-releases"
			url:  "https://api.github.com/repos/ajeetdsouza/zoxide/releases"
			asset: {
				amd64: "https://github.com/ajeetdsouza/zoxide/releases/download/v{version}/zoxide-{version}-x86_64-unknown-linux-musl.tar.gz"
				arm64: "https://github.com/ajeetdsouza/zoxide/releases/download/v{version}/zoxide-{version}-aarch64-unknown-linux-musl.tar.gz"
			}
		}
	}]
}
