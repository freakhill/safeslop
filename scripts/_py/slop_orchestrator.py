#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""slop orchestrator (Phase D MVP — host-only).

Reads a `slop.cue` from the repo root (or current directory) and acts on
the profiles declared inside. For now only `environment: host` profiles
are supported — `container` and `vm` come in Phase E. The runtime is a
pure-stdlib CLI (no Textual, no PEP-723 third-party deps) so it can run
without a TTY and without uv pulling new packages.

Subcommands:
    slop-orchestrator validate
    slop-orchestrator list
    slop-orchestrator run [<profile>]
    slop-orchestrator down

The fish wrapper `scripts/slop.fish` dispatches to this module when
`./slop.cue` exists; users normally type `slop run review`, not the
orchestrator binary directly.

The orchestrator composes existing scripts; it never reimplements
their work. Provisioning credentials → `slop-gh-key here create-pair`
etc.; launching the agent → `slop-agents claude/opencode`; cleanup →
the same scripts' `here cleanup` / `here unbind` flows. State of
running profiles is stored at `<repo>/.slop/state.json`.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

REPO_ROOT_ENV = "ATB_USER_PWD"

# Resolved at runtime: this file lives at <repo>/scripts/_py/, so the
# repo's policy module is at parents[2] / "library/layer/policy".
SOURCE_REPO_ROOT = Path(__file__).resolve().parents[2]
POLICY_MODULE = SOURCE_REPO_ROOT / "library" / "layer" / "policy"

DEFAULT_STATE_DIR = ".slop"
STATE_FILE = "state.json"


# ---------------------------------------------------------------------------
# Errors + small helpers
# ---------------------------------------------------------------------------


class OrchestratorError(Exception):
    """Anything we expect users to read on stderr."""


def _die(msg: str, code: int = 1) -> None:
    sys.stderr.write(f"slop: {msg}\n")
    sys.exit(code)


def _user_cwd() -> Path:
    """Where the user actually invoked `slop` from. The conf.d wrapper
    sets ATB_USER_PWD before exec; outside that wrapper we fall back to
    Python's cwd."""
    return Path(os.environ.get(REPO_ROOT_ENV) or os.getcwd())


def _git_toplevel(start: Path) -> Path | None:
    try:
        out = subprocess.check_output(
            ["git", "-C", str(start), "rev-parse", "--show-toplevel"],
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
        return Path(out) if out else None
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None


def _resolve_repo_root() -> Path:
    """Walk up from the user's cwd to a directory containing slop.cue.
    Falls back to the git toplevel, then cwd."""
    start = _user_cwd().resolve()
    cur = start
    while cur != cur.parent:
        if (cur / "slop.cue").is_file():
            return cur
        cur = cur.parent
    # Not found by walk — try git toplevel as a last shot.
    top = _git_toplevel(start)
    if top is not None and (top / "slop.cue").is_file():
        return top
    return start


def _slop_cue_path(repo_root: Path) -> Path:
    return repo_root / "slop.cue"


# ---------------------------------------------------------------------------
# CUE evaluation
# ---------------------------------------------------------------------------


def _evaluate_slop_cue(slop_cue: Path) -> dict[str, Any]:
    """Resolve the user's slop.cue against the bundled schema + presets.

    The user's file imports `slop.dev/isolation/{schema,presets}`. CUE
    resolves imports via the nearest cue.mod, which lives under
    library/layer/policy/. We can't make CUE find it from an arbitrary
    user repo, so we copy the user's slop.cue into a fresh subdir of
    the policy module, run `cue export` there, and tear the subdir down.
    """
    if not shutil.which("cue"):
        raise OrchestratorError(
            "`cue` not found on PATH. Install: brew install cue-lang/tap/cue"
        )
    runtime_root = POLICY_MODULE / ".runtime"
    runtime_root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=runtime_root, prefix="slop-") as td:
        td_path = Path(td)
        shutil.copy(slop_cue, td_path / "slop.cue")
        proc = subprocess.run(
            ["cue", "export", "--out", "json", "."],
            cwd=td_path,
            capture_output=True,
            text=True,
        )
        if proc.returncode != 0:
            raise OrchestratorError(f"cue export failed:\n{proc.stderr.rstrip()}")
        try:
            return json.loads(proc.stdout)
        except json.JSONDecodeError as e:
            raise OrchestratorError(
                f"cue produced unparseable JSON: {e}\n{proc.stdout[:200]}"
            ) from e


# ---------------------------------------------------------------------------
# Profile model (mirrors schema.cue's #Profile)
# ---------------------------------------------------------------------------


@dataclass
class Profile:
    name: str
    agent: str
    environment: str
    isolation: dict[str, Any] = field(default_factory=dict)
    credentials: dict[str, str] = field(default_factory=dict)
    on_exit: list[str] = field(default_factory=list)
    image: dict[str, Any] = field(default_factory=dict)


@dataclass
class SlopConfig:
    profiles: dict[str, Profile]
    default: str | None
    state_dir: str


def _parse_config(raw: dict[str, Any]) -> SlopConfig:
    if "profiles" not in raw or not isinstance(raw["profiles"], dict):
        raise OrchestratorError("slop.cue: top-level `profiles` map is required")
    profiles: dict[str, Profile] = {}
    for name, p in raw["profiles"].items():
        if not isinstance(p, dict):
            raise OrchestratorError(f"slop.cue: profile {name!r} is not a struct")
        for required in ("agent", "environment"):
            if required not in p:
                raise OrchestratorError(
                    f"slop.cue: profile {name!r} missing `{required}`"
                )
        profiles[name] = Profile(
            name=name,
            agent=p["agent"],
            environment=p["environment"],
            isolation=p.get("isolation") or {},
            credentials=p.get("credentials") or {},
            on_exit=list(p.get("on-exit") or []),
            image=p.get("image") or {},
        )
    default = raw.get("default")
    if default is not None and default not in profiles:
        raise OrchestratorError(
            f"slop.cue: default {default!r} is not a declared profile"
        )
    state_dir = raw.get("state-dir") or DEFAULT_STATE_DIR
    return SlopConfig(profiles=profiles, default=default, state_dir=state_dir)


def _resolve_profile(cfg: SlopConfig, name: str | None) -> Profile:
    if name is not None:
        if name not in cfg.profiles:
            raise OrchestratorError(
                f"unknown profile {name!r}. "
                f"Declared: {', '.join(sorted(cfg.profiles)) or '(none)'}"
            )
        return cfg.profiles[name]
    if cfg.default is not None:
        return cfg.profiles[cfg.default]
    if len(cfg.profiles) == 1:
        return next(iter(cfg.profiles.values()))
    raise OrchestratorError(
        f"slop.cue declares {len(cfg.profiles)} profiles and no `default`. "
        f"Pick one: slop run <name>. Available: "
        f"{', '.join(sorted(cfg.profiles))}"
    )


# ---------------------------------------------------------------------------
# State
# ---------------------------------------------------------------------------


@dataclass
class ProfileState:
    started_at: str
    credentials: dict[str, dict[str, Any]] = field(default_factory=dict)


def _state_path(repo_root: Path, state_dir: str) -> Path:
    return repo_root / state_dir / STATE_FILE


def _load_state(repo_root: Path, state_dir: str) -> dict[str, ProfileState]:
    p = _state_path(repo_root, state_dir)
    if not p.is_file():
        return {}
    raw = json.loads(p.read_text())
    return {
        name: ProfileState(
            started_at=v.get("started_at", ""),
            credentials=v.get("credentials", {}),
        )
        for name, v in raw.get("active_profiles", {}).items()
    }


def _save_state(
    repo_root: Path,
    state_dir: str,
    active: dict[str, ProfileState],
) -> None:
    p = _state_path(repo_root, state_dir)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(
        json.dumps(
            {
                "active_profiles": {
                    name: {
                        "started_at": s.started_at,
                        "credentials": s.credentials,
                    }
                    for name, s in active.items()
                }
            },
            indent=2,
        )
        + "\n"
    )


# ---------------------------------------------------------------------------
# Provisioning + launch
# ---------------------------------------------------------------------------


SLOP_GH_KEY = SOURCE_REPO_ROOT / "scripts" / "slop-gh-key.fish"
SLOP_FORGEJO_KEY = SOURCE_REPO_ROOT / "scripts" / "slop-forgejo-key.fish"
SLOP_RADICLE = SOURCE_REPO_ROOT / "scripts" / "slop-radicle.fish"
SLOP_AGENTS = SOURCE_REPO_ROOT / "scripts" / "slop-agents.fish"
SLOP_AGENT_SANDBOX_TOOLS = (
    SOURCE_REPO_ROOT / "scripts" / "slop-agent-sandbox-tools.fish"
)
SLOP_ISOLATE = SOURCE_REPO_ROOT / "scripts" / "slop-isolate.fish"
SLOP_BREW_VM = SOURCE_REPO_ROOT / "scripts" / "slop-brew-vm.fish"
COMPOSE_FILE = (
    SOURCE_REPO_ROOT / "library" / "layer" / "container" / "docker-compose.yml"
)
COMPOSE_ENV_FILE = (
    SOURCE_REPO_ROOT
    / "library"
    / "layer"
    / "container"
    / "agent-tools.env"
)
COMPOSE_ENV_EXAMPLE = (
    SOURCE_REPO_ROOT
    / "library"
    / "layer"
    / "container"
    / "agent-tools.env.example"
)
# Where the staged .ssh/ gets mounted inside the agent-tools container.
# The container's Dockerfile.agent has no USER directive, so it runs as
# root; root's HOME is /root.
CONTAINER_SSH_HOME = "/root/.ssh"


def _fish_run(script: Path, *cmd: str) -> subprocess.CompletedProcess[str]:
    """Source a fish module and run a command in it. Captures stdout for
    parsing; stderr is left attached so the user sees errors live."""
    inner = f"source {shlex.quote(str(script))}; {' '.join(shlex.quote(c) for c in cmd)}"
    return subprocess.run(
        ["fish", "-c", inner],
        capture_output=True,
        text=True,
    )


def _fish_exec(script: Path, *cmd: str) -> int:
    """Source and exec interactively (no capture) — used for `slop-agents
    claude` so the user gets the agent REPL with a real ctty."""
    inner = f"source {shlex.quote(str(script))}; {' '.join(shlex.quote(c) for c in cmd)}"
    return subprocess.call(["fish", "-c", inner])


_KEY_ID_RX = re.compile(r"^\s*id:\s*(\d+)\s*$", re.MULTILINE)


def _parse_create_pair_ids(stdout: str) -> list[int]:
    """Pull the integer key ids out of slop-{gh,forgejo}-key
    `here create-pair` stdout. The fish helpers print:
        Created ro deploy key
          repo: owner/repo
          id: 12345
          ...
    one block per access mode, so a successful create-pair emits
    exactly two ids. We return them in stdout order so the caller
    knows ro comes first."""
    return [int(m.group(1)) for m in _KEY_ID_RX.finditer(stdout)]


def _provision_credentials(profile: Profile) -> dict[str, dict[str, Any]]:
    """Create ephemeral credentials per the profile's `credentials`
    field. Returns a state-snippet describing what was created so
    cleanup can find it.

    Captures the just-created key ids so the on-exit
    `revoke-credentials` hook can revoke each one by id rather than
    falling through to `here cleanup` (= `revoke-expired --yes`),
    which is a no-op on freshly-issued 24h-TTL keys."""
    snapshot: dict[str, dict[str, Any]] = {}
    gh = profile.credentials.get("github")
    if gh and gh != "none":
        # ephemeral-ro / ephemeral-rw / ephemeral-pair all map to
        # `slop-gh-key here create-pair` today (the script always
        # creates both keys; per-mode selection is a future refinement).
        proc = _fish_run(SLOP_GH_KEY, "here", "create-pair")
        if proc.returncode != 0:
            raise OrchestratorError(
                f"slop-gh-key here create-pair failed (exit {proc.returncode})"
            )
        snapshot["github"] = {
            "mode": gh,
            "via": "slop-gh-key here create-pair",
            "key_ids": _parse_create_pair_ids(proc.stdout),
        }
    fj = profile.credentials.get("forgejo")
    if fj and fj != "none":
        proc = _fish_run(SLOP_FORGEJO_KEY, "here", "create-pair")
        if proc.returncode != 0:
            raise OrchestratorError(
                f"slop-forgejo-key here create-pair failed (exit {proc.returncode})"
            )
        snapshot["forgejo"] = {
            "mode": fj,
            "via": "slop-forgejo-key here create-pair",
            "key_ids": _parse_create_pair_ids(proc.stdout),
        }
    rad = profile.credentials.get("radicle")
    if rad and rad != "none":
        # Radicle's identity creation is name-driven, not repo-driven.
        # Use the active profile's name as the identity label.
        proc = _fish_run(
            SLOP_RADICLE, "create-identity", "--name", profile.name, "--ttl", "24h"
        )
        if proc.returncode != 0:
            raise OrchestratorError(
                f"slop-radicle create-identity failed (exit {proc.returncode})"
            )
        snapshot["radicle"] = {"mode": rad, "name": profile.name}
    return snapshot


def _launch_host(profile: Profile, dry_run: bool = False) -> int:
    """Exec the agent REPL in the user's cwd. Composes slop-agents."""
    if profile.agent == "claude":
        cli = "slop-agents claude"
    elif profile.agent == "opencode":
        cli = "slop-agents opencode"
    else:
        raise OrchestratorError(
            f"agent {profile.agent!r} not yet supported on environment=host. "
            f"Add it to scripts/slop-agents.fish or use environment=container."
        )
    print(f"slop: equivalent CLI: {cli}")
    if dry_run:
        return 0
    return _fish_exec(SLOP_AGENTS, profile.agent)


# ---------------------------------------------------------------------------
# Credential plumbing into containers
# ---------------------------------------------------------------------------


def _stage_runtime_dir(repo_root: Path, state_dir: str, profile_name: str) -> Path:
    return repo_root / state_dir / "runtime" / profile_name


def _newest_keypair(ssh_dir: Path, prefix: str) -> tuple[Path, Path] | None:
    """Find the most-recent (priv, pub) pair matching prefix*. Returns
    None when either side is missing (mismatched key files indicate a
    half-finished create-pair run; safer to skip than guess)."""
    privs = [
        p for p in ssh_dir.glob(f"{prefix}*")
        if not p.name.endswith(".pub")
    ]
    if not privs:
        return None
    priv = max(privs, key=lambda p: p.stat().st_mtime)
    pub = priv.with_name(priv.name + ".pub")
    if not pub.exists():
        return None
    return priv, pub


def _read_forgejo_hostname(ssh_dir: Path, ro_key_name: str) -> str | None:
    """Parse ~/.ssh/config to find the HostName declared for the most
    recent slop-forgejo-key marker block whose IdentityFile matches
    `ro_key_name`. Returns None if no matching block is found.

    The host-side `slop-forgejo-key here create-pair --install-ssh-config`
    writes a marker-fenced block per repo+session containing the real
    forgejo-instance hostname. We can't hardcode "forgejo.com" the way
    we do "github.com" — Codeberg, self-hosted forgejos, and gitea
    instances all coexist."""
    config_path = ssh_dir / "config"
    if not config_path.is_file():
        return None
    in_block = False
    matched_block = False
    candidate_host: str | None = None
    for raw in config_path.read_text().splitlines():
        line = raw.strip()
        if line.startswith("# BEGIN slop-forgejo-key:"):
            in_block = True
            matched_block = False
            candidate_host = None
            continue
        if line.startswith("# END slop-forgejo-key:") and matched_block:
            return candidate_host
        if in_block and line.startswith("# END slop-forgejo-key:"):
            in_block = False
            continue
        if not in_block:
            continue
        if line.startswith("HostName "):
            candidate_host = line.split(maxsplit=1)[1]
        if ro_key_name in line:
            matched_block = True
    return None


def _stage_credentials(
    profile: Profile,
    repo_root: Path,
    state_dir: str,
) -> Path | None:
    """Copy the ephemeral keypairs declared by `profile.credentials`
    into a per-profile staging dir and emit a container-local SSH
    config that maps the same `<host>-llm-{ro,rw}` aliases the host
    has, but with paths pointing at the in-container mount.

    Today: github, forgejo, and radicle.

      - github / forgejo: pair-shaped (`llm_agent_<host>_{ro,rw}_*`),
        gets an SSH `Host <host>-llm-{ro,rw}` alias block in the
        staged config. github HostName is hardcoded `github.com`;
        forgejo HostName is parsed from the host's ~/.ssh/config
        marker block since Forgejo / Codeberg / self-hosted instances
        vary.
      - radicle: single-key-shaped (`llm_agent_radicle_<name>_*`).
        No SSH alias entry — radicle URLs are `rad://...`, not
        ssh-host-aliased — but the keypair is still copied to the
        staged dir so a `rad` CLI inside the container can pick it
        up via `RAD_KEYS_PATH=/root/.ssh/llm_agent_radicle_<name>`
        (set in the agent's startup config or the tailored
        Dockerfile). The orchestrator does not configure rad itself
        — that's the user's call once they've added rad to their
        image.

    Returns the staging dir path (the .ssh/ subdir specifically — that
    becomes the bind-mount source) or None when nothing was staged.

    The staged dir contains *only* files this orchestrator just
    provisioned. The host's permanent identities (id_ed25519, id_rsa,
    …) are deliberately excluded so the container never sees them.
    """
    cred = profile.credentials or {}
    gh = cred.get("github") or "none"
    fj = cred.get("forgejo") or "none"
    rad = cred.get("radicle") or "none"
    if gh == "none" and fj == "none" and rad == "none":
        return None

    ssh_dir = Path.home() / ".ssh"
    if not ssh_dir.is_dir():
        return None

    base = _stage_runtime_dir(repo_root, state_dir, profile.name)
    stage = base / ".ssh"
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir(parents=True, mode=0o700)

    config_lines: list[str] = [
        f"# Generated by slop-orchestrator for profile {profile.name!r}.",
        "# Mirrors the host's ~/.ssh/config aliases but with paths",
        "# pointing at the in-container mount.",
        "",
    ]
    staged_anything = False

    def _stage_family(
        family: str,
        mode: str,
        host_name: str | None,
    ) -> None:
        nonlocal staged_anything
        if mode == "none":
            return
        ro = _newest_keypair(ssh_dir, f"llm_agent_{family}_ro_")
        rw = _newest_keypair(ssh_dir, f"llm_agent_{family}_rw_")
        if ro is None or rw is None:
            sys.stderr.write(
                f"slop: credentials.{family} requested but "
                f"llm_agent_{family}_{{ro,rw}}_* files not found under "
                "~/.ssh/. Skipping that family in the container ssh-mount.\n"
            )
            return
        # Resolve HostName once per family; bail with a warning if the
        # forgejo config block isn't there (probably means the user
        # disabled --install-ssh-config or wiped their ~/.ssh/config).
        if host_name is None:
            host_name = _read_forgejo_hostname(ssh_dir, ro[0].name)
        if host_name is None:
            sys.stderr.write(
                f"slop: could not resolve {family} HostName from "
                "~/.ssh/config; skipping that family.\n"
            )
            return
        # Copy private + public for both ro and rw at the right modes.
        for priv, pub in (ro, rw):
            for variant, mode_bits in ((priv, 0o600), (pub, 0o644)):
                dst = stage / variant.name
                shutil.copy2(variant, dst)
                dst.chmod(mode_bits)
        # Append the alias block for this family.
        alias_prefix = f"{family}-llm" if family == "github" else f"{family}-llm"
        # ↑ today both families share the alias-prefix shape; left
        # explicit so a future per-host-prefix override is easy.
        config_lines.extend([
            f"Host {alias_prefix}-ro",
            f"  HostName {host_name}",
            "  User git",
            f"  IdentityFile ~/.ssh/{ro[0].name}",
            "  IdentitiesOnly yes",
            "",
            f"Host {alias_prefix}-rw",
            f"  HostName {host_name}",
            "  User git",
            f"  IdentityFile ~/.ssh/{rw[0].name}",
            "  IdentitiesOnly yes",
            "",
        ])
        staged_anything = True

    _stage_family("github", gh, "github.com")
    _stage_family("forgejo", fj, None)  # HostName parsed lazily

    # Radicle has no ro/rw split — single key per identity. We stage
    # only the most recent matching keypair; the agent inside the
    # container sets RAD_KEYS_PATH (or equivalent) to the file path,
    # the orchestrator does not configure rad's own state.
    if rad != "none":
        priv_candidates = [
            p for p in ssh_dir.glob("llm_agent_radicle_*")
            if not p.name.endswith(".pub")
        ]
        if not priv_candidates:
            sys.stderr.write(
                "slop: credentials.radicle requested but "
                "llm_agent_radicle_* files not found under ~/.ssh/. "
                "Skipping radicle in the staged dir.\n"
            )
        else:
            priv = max(priv_candidates, key=lambda p: p.stat().st_mtime)
            pub = priv.with_name(priv.name + ".pub")
            for variant, mode_bits in ((priv, 0o600), (pub, 0o644)):
                if not variant.exists():
                    continue
                dst = stage / variant.name
                shutil.copy2(variant, dst)
                dst.chmod(mode_bits)
            # Append a comment block (not an SSH Host stanza) telling
            # the user where the staged radicle key lives in-container.
            config_lines.extend([
                f"# radicle identity (no SSH alias — set in your agent's startup):",
                f"#   RAD_KEYS_PATH=~/.ssh/{priv.name}",
                "",
            ])
            staged_anything = True

    if not staged_anything:
        # Tear down the empty stage dir so the override doesn't bind
        # an empty .ssh/ over the container's default $HOME/.ssh.
        shutil.rmtree(stage, ignore_errors=True)
        return None

    config = stage / "config"
    config.write_text("\n".join(config_lines).rstrip() + "\n")
    config.chmod(0o644)
    return stage.resolve()


DEFAULT_IMAGE_BASE = "local/agent-sandbox-tools:latest"
TAILORED_TAG_PREFIX = "local/agent-sandbox-tools:slop-"


def _image_spec_hash(spec: dict, base_tag: str) -> str:
    """Content-hash an #ImageSpec so two profiles with the same extras
    deduplicate to the same tailored image. Sorted to make the hash
    order-insensitive for the package lists; the base tag is the only
    asymmetric input so it goes last in the input string."""
    parts = [
        ",".join(sorted(spec.get("extra-apt") or [])),
        ",".join(sorted(spec.get("extra-pip") or [])),
        ",".join(sorted(spec.get("extra-npm") or [])),
        base_tag,
    ]
    return hashlib.sha256("|".join(parts).encode()).hexdigest()[:12]


def _render_tailored_dockerfile(
    target: Path,
    base_tag: str,
    spec: dict,
) -> None:
    """Write a Dockerfile.tailored beside the staging tree that adds
    the requested package layers on top of the base image. Each apt
    invocation is a single RUN to keep the image small; pip and npm
    each get their own RUN so a failure surfaces with the right
    error context. No --no-cache flags — the docker build cache is
    fine here, and we content-hash the inputs so a different spec
    produces a different tag and a fresh build."""
    target.parent.mkdir(parents=True, exist_ok=True)
    apt = spec.get("extra-apt") or []
    pip = spec.get("extra-pip") or []
    npm = spec.get("extra-npm") or []
    lines = [f"FROM {base_tag}"]
    if apt:
        joined = " ".join(shlex.quote(p) for p in apt)
        lines.append(
            "RUN apt-get update "
            "&& apt-get install -y --no-install-recommends "
            f"{joined} "
            "&& rm -rf /var/lib/apt/lists/*"
        )
    if pip:
        joined = " ".join(shlex.quote(p) for p in pip)
        lines.append(f"RUN uv pip install --system --no-cache {joined}")
    if npm:
        joined = " ".join(shlex.quote(p) for p in npm)
        lines.append(f"RUN npm install -g {joined}")
    target.write_text("\n".join(lines) + "\n")


def _docker_image_exists(tag: str) -> bool:
    """True iff the local Docker daemon already has an image with the
    given tag. Used so repeated runs with the same spec skip the
    docker build round-trip."""
    if not shutil.which("docker"):
        return False
    proc = subprocess.run(
        ["docker", "image", "inspect", tag],
        capture_output=True,
    )
    return proc.returncode == 0


def _resolve_image_tag(
    profile: Profile,
    repo_root: Path,
    state_dir: str,
) -> tuple[str | None, Path | None]:
    """Return the image tag to point the agent-tools service at.

    - No image spec / no extras → (None, None); caller uses the default
      compose-built tag.
    - Spec with `base` only → (base, None); just the override.
    - Spec with extras → (tailored-tag, dockerfile-path); the caller
      docker-builds the dockerfile if the tag isn't already cached
      locally."""
    spec = profile.image or {}
    base = spec.get("base") or DEFAULT_IMAGE_BASE
    has_extras = any(
        spec.get(k) for k in ("extra-apt", "extra-pip", "extra-npm")
    )
    if not has_extras and not spec.get("base"):
        return (None, None)
    if not has_extras:
        return (base, None)
    digest = _image_spec_hash(spec, base)
    tag = f"{TAILORED_TAG_PREFIX}{digest}"
    dockerfile = _stage_runtime_dir(repo_root, state_dir, profile.name) / "Dockerfile.tailored"
    return (tag, dockerfile)


def _build_tailored_image(
    tag: str,
    dockerfile: Path,
    base_tag: str,
    spec: dict,
) -> None:
    """Write the Dockerfile and run `docker build` to produce the
    tailored tag. Idempotent: if the tag already exists locally we
    skip the build entirely."""
    if _docker_image_exists(tag):
        return
    _render_tailored_dockerfile(dockerfile, base_tag, spec)
    proc = subprocess.run(
        [
            "docker", "build",
            "-t", tag,
            "-f", str(dockerfile),
            str(dockerfile.parent),
        ],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        raise OrchestratorError(
            f"docker build of tailored image {tag} failed "
            f"(exit {proc.returncode}):\n{proc.stderr.rstrip()}"
        )


def _render_compose_override(
    stage_ssh: Path | None,
    image_tag: str | None = None,
    target_service: str = "agent-tools",
    parent_dir: Path | None = None,
) -> Path:
    """Generate a docker-compose.override.yml that may:

      - bind-mount staged SSH keys at /root/.ssh (when stage_ssh is set),
      - swap the agent-tools image: field to a tailored tag (when
        image_tag is set).

    Returns the override file path so the caller can pass it as a
    second `-f` flag to docker compose.

    `parent_dir` controls where the override is written. Defaults to
    stage_ssh.parent for back-compat with the credential-only path;
    pass it explicitly when there is no stage_ssh (image-only
    overrides) so the file lands under the per-profile runtime dir.
    """
    if parent_dir is None:
        if stage_ssh is None:
            raise OrchestratorError(
                "_render_compose_override needs either stage_ssh or parent_dir"
            )
        parent_dir = stage_ssh.parent
    override = parent_dir / "docker-compose.override.yml"
    lines = [
        "# Generated by slop-orchestrator. Layered patches to the",
        "# committed library/layer/container/docker-compose.yml so the",
        "# committed file stays untouched while a profile gets its own",
        "# isolation knobs (staged SSH keys + tailored image tag).",
        "services:",
        f"  {target_service}:",
    ]
    if image_tag is not None:
        lines.append(f"    image: {image_tag}")
    if stage_ssh is not None:
        lines.append("    volumes:")
        lines.append(f"      - {stage_ssh}:{CONTAINER_SSH_HOME}:ro")
    override.write_text("\n".join(lines) + "\n")
    return override


def _wipe_runtime_dir(repo_root: Path, state_dir: str, profile_name: str) -> None:
    """Remove the .slop/runtime/<profile>/ tree if present. Idempotent."""
    base = _stage_runtime_dir(repo_root, state_dir, profile_name)
    if base.exists():
        shutil.rmtree(base, ignore_errors=True)


def _launch_container(
    profile: Profile,
    dry_run: bool = False,
    repo_root: Path | None = None,
    state_dir: str | None = None,
) -> int:
    """Bring the agent-tools stack up (idempotent build + proxy start)
    via the existing slop-agent-sandbox-tools flow, stage any ephemeral
    SSH keys requested by the profile into a per-profile bind-mount,
    then drop the user into the agent REPL inside the container.

    Composes:
      slop-agent-sandbox-tools up                     # build + proxy
      <stage credentials>                             # if profile asks
      docker compose -f main -f override run --rm     # mounts the staged keys

    The image-presence check, FROM-dependency build order, and proxy
    lifecycle are handled by slop-agent-sandbox-tools — we never reach
    into docker for those.

    Credential plumbing: when `credentials.github != "none"`, the
    orchestrator copies the most-recent llm_agent_github_{ro,rw}_*
    keypair into <state_dir>/runtime/<profile>/.ssh/ and writes a
    fresh SSH config there that maps the `github-llm-ro/-rw` aliases
    to the staged filenames. A docker-compose override file beside it
    bind-mounts that .ssh/ at /root/.ssh in the agent-tools service
    read-only. Pushes from inside the container resolve the aliases
    against the staged keys, never touching the host's permanent
    identities.

    Forgejo and Radicle credential plumbing into the container is a
    follow-up; today only github is staged. Forgejo follows the
    identical filename pattern and would slot in cleanly here.
    """
    if profile.agent not in ("claude", "opencode"):
        raise OrchestratorError(
            f"agent {profile.agent!r} not yet supported on environment=container. "
            "The container ships claude + opencode preinstalled; for other "
            "agents, drop into `slop-agent-sandbox-tools shell` and run them by hand."
        )

    # Stage credentials before announcing the equivalent CLI so the
    # printed command line reflects what will actually run.
    stage_ssh: Path | None = None
    image_tag: str | None = None
    dockerfile: Path | None = None
    override: Path | None = None
    have_runtime = repo_root is not None and state_dir is not None
    if have_runtime and not dry_run:
        stage_ssh = _stage_credentials(profile, repo_root, state_dir)
        image_tag, dockerfile = _resolve_image_tag(
            profile, repo_root, state_dir
        )
        if dockerfile is not None and image_tag is not None:
            base_tag = profile.image.get("base") or DEFAULT_IMAGE_BASE
            _build_tailored_image(image_tag, dockerfile, base_tag, profile.image)
        if stage_ssh is not None or image_tag is not None:
            parent = (
                stage_ssh.parent
                if stage_ssh is not None
                else _stage_runtime_dir(repo_root, state_dir, profile.name)
            )
            parent.mkdir(parents=True, exist_ok=True)
            override = _render_compose_override(
                stage_ssh, image_tag, parent_dir=parent
            )

    cli_up = "slop-agent-sandbox-tools up"
    if override is not None:
        cli_run = (
            f"docker compose -f {COMPOSE_FILE} -f {override} run --rm "
            f"agent-tools {profile.agent}"
        )
    else:
        cli_run = f"slop-agent-sandbox-tools run {profile.agent}"
    print(f"slop: equivalent CLI: {cli_up} && {cli_run}")
    if dry_run:
        # Dry-run path mirrors the credential-gated + image-tailored
        # branches by describing what *would* be staged.
        cred = profile.credentials or {}
        families = [
            family for family in ("github", "forgejo", "radicle")
            if cred.get(family) and cred[family] != "none"
        ]
        if families:
            print(
                f"slop: would stage ephemeral {' + '.join(families)} keys at "
                "<state-dir>/runtime/<profile>/.ssh/ and bind-mount them "
                f"at {CONTAINER_SSH_HOME} in the agent-tools service."
            )
        spec = profile.image or {}
        if any(spec.get(k) for k in ("extra-apt", "extra-pip", "extra-npm")):
            base = spec.get("base") or DEFAULT_IMAGE_BASE
            digest = _image_spec_hash(spec, base)
            print(
                f"slop: would build tailored image "
                f"{TAILORED_TAG_PREFIX}{digest} "
                f"FROM {base} layering apt={spec.get('extra-apt') or []} "
                f"pip={spec.get('extra-pip') or []} "
                f"npm={spec.get('extra-npm') or []}."
            )
        return 0
    if not shutil.which("docker"):
        raise OrchestratorError(
            "`docker` not found on PATH. Install Docker / OrbStack / Lima before "
            "running container profiles, or change the profile to environment=host."
        )
    proc = _fish_run(SLOP_AGENT_SANDBOX_TOOLS, "up")
    if proc.returncode != 0:
        raise OrchestratorError(
            f"slop-agent-sandbox-tools up failed (exit {proc.returncode}):\n"
            f"{proc.stderr.rstrip()}"
        )
    if override is None:
        # No credentials to plumb — defer to the existing wrapper.
        return _fish_exec(SLOP_AGENT_SANDBOX_TOOLS, "run", profile.agent)
    # Credential-gated path: invoke compose directly with both -f files
    # so the override is actually applied. We mirror the wrapper's
    # env-file handling so pinned versions still flow through.
    argv: list[str] = ["docker", "compose"]
    if COMPOSE_ENV_FILE.is_file():
        argv += ["--env-file", str(COMPOSE_ENV_FILE)]
    argv += ["-f", str(COMPOSE_FILE), "-f", str(override)]
    argv += ["run", "--rm", "agent-tools", profile.agent]
    return subprocess.call(argv)


def _launch_vm(
    profile: Profile,
    dry_run: bool = False,
    repo_root: Path | None = None,
    state_dir: str | None = None,
) -> int:
    """Provision a disposable Tart VM and run the agent inside.

    Composes:
      slop-brew-vm init                          # clone + boot the session VM
      <stage credentials>                        # if profile asks
      slop-brew-vm copy-in <stage> ~/.ssh        # plumb keys into the guest
      slop-brew-vm run <agent>                   # exec the agent inside

    The VM (`brew-sandbox-session` by default) is expected to have the
    agent pre-installed — typically via a one-time
    `slop-brew-vm install <agent>` against the trusted base template,
    or by baking it into the source image. The orchestrator does not
    auto-install: VMs are heavy and one-shot installs would surprise
    the user. If the agent isn't on PATH inside the VM, `slop-brew-vm
    run` fails with "command not found"; on-exit destroy-vm still
    runs to leave no stale state.

    Credential plumbing: same staging logic as the container path.
    The staged .ssh/ goes into the guest via `slop-brew-vm copy-in`
    rather than a bind mount; scp -r preserves the 0600/0644 mode
    bits the staging helper set, so the guest's ssh client accepts
    the IdentityFile entries without complaint. The guest path is
    `~/.ssh`, which scp expands to the BREW_VM_SSH_USER's home
    regardless of the guest OS (macOS Tart base images use /Users/<user>;
    Linux guests use /home/<user>)."""
    if profile.agent not in ("claude", "opencode"):
        raise OrchestratorError(
            f"agent {profile.agent!r} not yet supported on environment=vm. "
            "Pre-install your agent in the brew-vm trusted base template "
            "(`slop-brew-vm install <agent>`) and rerun."
        )

    stage_ssh: Path | None = None
    if repo_root is not None and state_dir is not None and not dry_run:
        stage_ssh = _stage_credentials(profile, repo_root, state_dir)

    # Predict whether we would stage credentials for the dry-run path
    # so the printed equivalent CLI reflects what would happen.
    cred = profile.credentials or {}
    would_stage = any(
        cred.get(family) and cred[family] != "none"
        for family in ("github", "forgejo")
    )

    cli_parts = ["slop-brew-vm init"]
    if stage_ssh is not None:
        cli_parts.append(f"slop-brew-vm copy-in {stage_ssh} ~/.ssh")
    elif dry_run and would_stage:
        cli_parts.append(
            "slop-brew-vm copy-in <state-dir>/runtime/<profile>/.ssh ~/.ssh"
        )
    cli_parts.append(f"slop-brew-vm run {profile.agent}")
    print(f"slop: equivalent CLI: {' && '.join(cli_parts)}")

    if dry_run:
        cred = profile.credentials or {}
        families = [
            family for family in ("github", "forgejo", "radicle")
            if cred.get(family) and cred[family] != "none"
        ]
        if families:
            print(
                f"slop: would stage ephemeral {' + '.join(families)} keys at "
                "<state-dir>/runtime/<profile>/.ssh/ and copy-in to ~/.ssh "
                "inside the VM via slop-brew-vm copy-in (scp -r)."
            )
        return 0
    if not shutil.which("tart"):
        raise OrchestratorError(
            "`tart` not found on PATH. Install: brew install cirruslabs/cli/tart, "
            "or change the profile to environment=container."
        )
    proc = _fish_run(SLOP_BREW_VM, "init")
    if proc.returncode != 0:
        raise OrchestratorError(
            f"slop-brew-vm init failed (exit {proc.returncode}):\n"
            f"{proc.stderr.rstrip()}"
        )
    if stage_ssh is not None:
        proc = _fish_run(
            SLOP_BREW_VM, "copy-in", str(stage_ssh), "~/.ssh"
        )
        if proc.returncode != 0:
            # Don't raise — VM is up and the agent might not need git.
            # Surface the failure and continue so the user sees what
            # happened in their terminal.
            sys.stderr.write(
                f"slop: slop-brew-vm copy-in failed ({proc.returncode}); "
                "agent will not see the staged keys. "
                f"{proc.stderr.rstrip()}\n"
            )
    return _fish_exec(SLOP_BREW_VM, "run", profile.agent)


def _snapshot_state(
    profile: Profile,
    state: ProfileState,
    repo_root: Path,
    state_dir: str,
) -> Path:
    """Write a post-mortem-friendly snapshot of the resolved profile +
    its captured state to <state-dir>/snapshots/<utc-stamp>.json before
    teardown wipes the runtime dir. Useful when a session goes wrong
    and you want to know what was provisioned: the captured key_ids,
    the agent + env, the on-exit hooks declared.

    Snapshots are append-only — we never delete them on `slop down`,
    only on the user explicitly removing `.slop/`. They're gitignored
    via the `.slop/` rule already in place."""
    snap_dir = repo_root / state_dir / "snapshots"
    snap_dir.mkdir(parents=True, exist_ok=True)
    stamp = time.strftime("%Y-%m-%dT%H-%M-%SZ", time.gmtime())
    out = snap_dir / f"{profile.name}-{stamp}.json"
    payload = {
        "snapshotted_at": stamp,
        "profile": {
            "name": profile.name,
            "agent": profile.agent,
            "environment": profile.environment,
            "credentials": profile.credentials,
            "on_exit": profile.on_exit,
            "image": profile.image,
        },
        "state": {
            "started_at": state.started_at,
            "credentials": state.credentials,
        },
    }
    out.write_text(json.dumps(payload, indent=2) + "\n")
    return out


def _on_exit_hooks(
    profile: Profile,
    state: ProfileState,
    repo_root: Path | None = None,
    state_dir: str | None = None,
) -> None:
    """Run profile.on_exit hooks idempotently. Errors print but don't
    abort the chain so partial cleanup still happens.

    `snapshot-state` runs *first* in declaration order regardless,
    because users typically declare it alongside teardown hooks
    (revoke-credentials / stop-container) — capturing what was
    provisioned needs to happen before cleanup destroys the evidence.
    """
    # Run snapshot-state first, regardless of where it appears in the
    # declaration list — the alternative is forcing users to remember
    # to put it before revoke-credentials, which is footgun-prone.
    if "snapshot-state" in profile.on_exit:
        if repo_root is not None and state_dir is not None:
            try:
                out = _snapshot_state(profile, state, repo_root, state_dir)
                print(f"slop: snapshotted state → {out}")
            except OSError as e:
                sys.stderr.write(f"slop: snapshot-state failed: {e}\n")
        else:
            sys.stderr.write(
                "slop: snapshot-state requested but no state-dir context "
                "(orchestrator was invoked with the on-exit chain detached "
                "from a repo). Skipping.\n"
            )
    for hook in profile.on_exit:
        if hook == "revoke-credentials":
            _revoke_credentials(state)
        elif hook == "stop-container":
            if profile.environment != "container":
                sys.stderr.write(
                    "slop: stop-container is a no-op when environment != container\n"
                )
                continue
            proc = _fish_run(SLOP_AGENT_SANDBOX_TOOLS, "down")
            if proc.returncode != 0:
                sys.stderr.write(
                    f"slop: slop-agent-sandbox-tools down failed ({proc.returncode}): "
                    f"{proc.stderr.rstrip()}\n"
                )
        elif hook == "stop-proxy":
            # Targets the Envoy + CoreDNS + notifier stack from
            # slop-isolate (separate from the squid sidecar that
            # `slop-agent-sandbox-tools down` already tears down with
            # the container stack).
            proc = _fish_run(SLOP_ISOLATE, "proxy", "stop")
            if proc.returncode != 0:
                sys.stderr.write(
                    f"slop: slop-isolate proxy stop failed ({proc.returncode}): "
                    f"{proc.stderr.rstrip()}\n"
                )
        elif hook == "destroy-vm":
            if profile.environment != "vm":
                sys.stderr.write(
                    "slop: destroy-vm is a no-op when environment != vm\n"
                )
                continue
            proc = _fish_run(SLOP_BREW_VM, "destroy")
            if proc.returncode != 0:
                sys.stderr.write(
                    f"slop: slop-brew-vm destroy failed ({proc.returncode}): "
                    f"{proc.stderr.rstrip()}\n"
                )
        elif hook == "snapshot-state":
            # Already handled before the loop, in declaration-order-
            # independent fashion. Nothing more to do.
            pass
        else:
            sys.stderr.write(f"slop: unknown on-exit hook {hook!r}; skipping\n")


def _revoke_credentials(state: ProfileState) -> None:
    """Run the matching `here revoke <id>` per captured key, falling
    back to `here cleanup` if the state has no key_ids (older state
    files, or a manual edit that dropped them). The fallback is
    `revoke-expired --yes`, which is a no-op on freshly-issued 24h-TTL
    keys — so without per-id revoke the on-exit hook used to leave
    those keys live until they aged out, defeating the point of
    per-session ephemeral creds."""
    for family, script in (("github", SLOP_GH_KEY), ("forgejo", SLOP_FORGEJO_KEY)):
        snap = state.credentials.get(family)
        if not snap:
            continue
        ids = snap.get("key_ids") or []
        if ids:
            for kid in ids:
                proc = _fish_run(script, "here", "revoke", str(kid))
                if proc.returncode != 0:
                    sys.stderr.write(
                        f"slop: {family}-key revoke {kid} failed "
                        f"({proc.returncode}): {proc.stderr.rstrip()}\n"
                    )
        else:
            proc = _fish_run(script, "here", "cleanup")
            if proc.returncode != 0:
                sys.stderr.write(
                    f"slop: {family}-key cleanup failed ({proc.returncode}): "
                    f"{proc.stderr.rstrip()}\n"
                )
    if "radicle" in state.credentials:
        proc = _fish_run(SLOP_RADICLE, "retire-expired", "--yes")
        if proc.returncode != 0:
            sys.stderr.write(
                f"slop: radicle retire-expired failed ({proc.returncode}): "
                f"{proc.stderr.rstrip()}\n"
            )


# ---------------------------------------------------------------------------
# Subcommands
# ---------------------------------------------------------------------------


def _load_for_user() -> tuple[Path, SlopConfig]:
    repo_root = _resolve_repo_root()
    slop_cue = _slop_cue_path(repo_root)
    if not slop_cue.is_file():
        raise OrchestratorError(
            f"no slop.cue found (looked at {slop_cue}). "
            "Drop one in your repo root, or run `slop` without args for the TUI."
        )
    raw = _evaluate_slop_cue(slop_cue)
    cfg = _parse_config(raw)
    return repo_root, cfg


def cmd_validate(_args: argparse.Namespace) -> int:
    repo_root, cfg = _load_for_user()
    print(f"slop.cue: {repo_root / 'slop.cue'}")
    print(f"profiles: {len(cfg.profiles)} ({', '.join(sorted(cfg.profiles))})")
    if cfg.default:
        print(f"default:  {cfg.default}")
    return 0


def cmd_list(_args: argparse.Namespace) -> int:
    repo_root, cfg = _load_for_user()
    state = _load_state(repo_root, cfg.state_dir)
    for name in sorted(cfg.profiles):
        p = cfg.profiles[name]
        marker = "*" if name == cfg.default else " "
        is_active = "active" if name in state else "idle"
        print(
            f"{marker} {name:<20} agent={p.agent:<10} env={p.environment:<10} "
            f"creds={','.join(k for k, v in p.credentials.items() if v != 'none') or '-'} "
            f"[{is_active}]"
        )
    return 0


def cmd_run(args: argparse.Namespace) -> int:
    repo_root, cfg = _load_for_user()
    profile = _resolve_profile(cfg, args.profile)
    if profile.environment not in ("host", "container", "vm"):
        raise OrchestratorError(
            f"profile {profile.name!r} declares unknown environment "
            f"{profile.environment!r}."
        )
    print(
        f"slop: launching profile {profile.name!r} "
        f"(agent={profile.agent}, env={profile.environment})"
    )
    if args.dry_run:
        # Show what would be provisioned without doing the work. Useful
        # in CI / on machines without docker / for plan review.
        print("slop: --dry-run set; provisioning + launch skipped.")
        if profile.credentials and any(
            v != "none" for v in profile.credentials.values()
        ):
            present = [
                k for k, v in profile.credentials.items() if v != "none"
            ]
            print(f"slop: would provision credentials: {', '.join(present)}")
        if profile.environment == "host":
            return _launch_host(profile, dry_run=True)
        if profile.environment == "container":
            return _launch_container(profile, dry_run=True)
        return _launch_vm(profile, dry_run=True)
    cred_snapshot = _provision_credentials(profile)
    state = _load_state(repo_root, cfg.state_dir)
    state[profile.name] = ProfileState(
        started_at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        credentials=cred_snapshot,
    )
    _save_state(repo_root, cfg.state_dir, state)
    if profile.environment == "host":
        rc = _launch_host(profile)
    elif profile.environment == "container":
        rc = _launch_container(
            profile,
            repo_root=repo_root,
            state_dir=cfg.state_dir,
        )
    else:
        rc = _launch_vm(
            profile,
            repo_root=repo_root,
            state_dir=cfg.state_dir,
        )
    # Run on-exit hooks once the agent exits. Reload state so concurrent
    # runs in another tty don't lose each other's snapshots.
    state = _load_state(repo_root, cfg.state_dir)
    if profile.name in state:
        _on_exit_hooks(
            profile,
            state[profile.name],
            repo_root=repo_root,
            state_dir=cfg.state_dir,
        )
        del state[profile.name]
        _save_state(repo_root, cfg.state_dir, state)
    # Always wipe the per-profile runtime staging dir (.ssh/ + override)
    # whether or not on-exit hooks ran — leaving private keys staged on
    # disk after the run would defeat the point of "ephemeral".
    _wipe_runtime_dir(repo_root, cfg.state_dir, profile.name)
    return rc


def cmd_down(_args: argparse.Namespace) -> int:
    repo_root, cfg = _load_for_user()
    state = _load_state(repo_root, cfg.state_dir)
    if not state:
        print("slop: no active profiles for this repo.")
        return 0
    for name, s in list(state.items()):
        if name not in cfg.profiles:
            sys.stderr.write(
                f"slop: state mentions profile {name!r} that is not in slop.cue; "
                "skipping cleanup.\n"
            )
            continue
        print(f"slop: cleaning up profile {name!r}")
        _on_exit_hooks(
            cfg.profiles[name],
            s,
            repo_root=repo_root,
            state_dir=cfg.state_dir,
        )
        _wipe_runtime_dir(repo_root, cfg.state_dir, name)
        del state[name]
    _save_state(repo_root, cfg.state_dir, state)
    return 0


# ---------------------------------------------------------------------------
# Argparse / entry point
# ---------------------------------------------------------------------------


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="slop-orchestrator",
        description="Run agents declared in slop.cue.",
    )
    sub = p.add_subparsers(dest="cmd", required=True)
    sub.add_parser("validate", help="Validate slop.cue against the bundled schema.")
    sub.add_parser("list", help="List declared profiles + their state.")
    p_run = sub.add_parser(
        "run",
        help="Run a profile. Supports environment: host or container.",
    )
    p_run.add_argument(
        "profile",
        nargs="?",
        default=None,
        help="Profile name. Omit to use `default`, or the only profile if there's one.",
    )
    p_run.add_argument(
        "--dry-run",
        action="store_true",
        help="Print what would be provisioned + launched, without side effects.",
    )
    sub.add_parser("down", help="Run on-exit hooks for active profiles.")
    return p


_DISPATCH = {
    "validate": cmd_validate,
    "list": cmd_list,
    "run": cmd_run,
    "down": cmd_down,
}


def main(argv: list[str] | None = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)
    handler = _DISPATCH[args.cmd]
    try:
        return handler(args)
    except OrchestratorError as e:
        _die(str(e))
        return 1  # _die exits, but linters want this


if __name__ == "__main__":
    sys.exit(main())
