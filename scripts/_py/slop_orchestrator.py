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
import json
import os
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


def _provision_credentials(profile: Profile) -> dict[str, dict[str, Any]]:
    """Create ephemeral credentials per the profile's `credentials`
    field. Returns a state-snippet describing what was created so
    cleanup can find it."""
    snapshot: dict[str, dict[str, Any]] = {}
    gh = profile.credentials.get("github")
    if gh and gh != "none":
        # ephemeral-ro / ephemeral-rw / ephemeral-pair all map to
        # `slop-gh-key here create-pair` today (the script always
        # creates both keys; per-mode selection is a Phase E refinement).
        proc = _fish_run(SLOP_GH_KEY, "here", "create-pair")
        if proc.returncode != 0:
            raise OrchestratorError(
                f"slop-gh-key here create-pair failed (exit {proc.returncode})"
            )
        snapshot["github"] = {"mode": gh, "via": "slop-gh-key here create-pair"}
    fj = profile.credentials.get("forgejo")
    if fj and fj != "none":
        proc = _fish_run(SLOP_FORGEJO_KEY, "here", "create-pair")
        if proc.returncode != 0:
            raise OrchestratorError(
                f"slop-forgejo-key here create-pair failed (exit {proc.returncode})"
            )
        snapshot["forgejo"] = {"mode": fj, "via": "slop-forgejo-key here create-pair"}
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


def _stage_github_credentials(profile: Profile, repo_root: Path, state_dir: str) -> Path | None:
    """Copy the most-recent ephemeral GitHub keypair into a per-profile
    staging dir and write a fresh SSH config that maps the
    `github-llm-{ro,rw}` aliases to the staged filenames. The directory
    becomes the bind-mount source for the container's /root/.ssh/.

    Returns the absolute staging dir path (the .ssh/ subdir specifically
    — that's what gets mounted), or None when there is nothing to
    plumb (no github creds requested, or no keys on disk yet).

    Why we stage instead of mounting ~/.ssh/ directly: the user's
    ~/.ssh/ contains permanent identities (id_ed25519, id_rsa, …) that
    we deliberately keep out of the sandbox per the isolation policy.
    Staging filters down to only the llm_agent_github_* files this
    profile created.
    """
    gh = profile.credentials.get("github")
    if not gh or gh == "none":
        return None

    ssh_dir = Path.home() / ".ssh"
    if not ssh_dir.is_dir():
        return None

    def _newest(prefix: str) -> Path | None:
        candidates = [
            p for p in ssh_dir.glob(f"{prefix}*")
            if not p.name.endswith(".pub")
        ]
        if not candidates:
            return None
        return max(candidates, key=lambda p: p.stat().st_mtime)

    ro_priv = _newest("llm_agent_github_ro_")
    rw_priv = _newest("llm_agent_github_rw_")
    if ro_priv is None or rw_priv is None:
        sys.stderr.write(
            "slop: credentials.github requested but llm_agent_github_{ro,rw}_* "
            "files not found under ~/.ssh/. Skipping container ssh-mount.\n"
        )
        return None

    base = _stage_runtime_dir(repo_root, state_dir, profile.name)
    stage = base / ".ssh"
    if stage.exists():
        # Wipe stale staging (a prior run that wasn't cleaned up).
        shutil.rmtree(stage)
    stage.mkdir(parents=True, mode=0o700)

    for src in (ro_priv, rw_priv):
        for variant in (src, src.with_name(src.name + ".pub")):
            if not variant.exists():
                continue
            dst = stage / variant.name
            shutil.copy2(variant, dst)
            dst.chmod(0o600 if variant.suffix != ".pub" else 0o644)

    # Container-local SSH config. Uses ~/.ssh/<filename> so the same
    # config works regardless of which user the container runs as.
    config = stage / "config"
    config.write_text(
        f"# Generated by slop-orchestrator for profile {profile.name!r}.\n"
        f"# Mirrors the host's ~/.ssh/config aliases but with paths\n"
        f"# pointing at the in-container mount.\n"
        f"\n"
        f"Host github-llm-ro\n"
        f"  HostName github.com\n"
        f"  User git\n"
        f"  IdentityFile ~/.ssh/{ro_priv.name}\n"
        f"  IdentitiesOnly yes\n"
        f"\n"
        f"Host github-llm-rw\n"
        f"  HostName github.com\n"
        f"  User git\n"
        f"  IdentityFile ~/.ssh/{rw_priv.name}\n"
        f"  IdentitiesOnly yes\n"
    )
    config.chmod(0o644)

    return stage.resolve()


def _render_compose_override(
    stage_ssh: Path,
    target_service: str = "agent-tools",
) -> Path:
    """Generate a docker-compose.override.yml beside the staged .ssh/
    that mounts it read-only at /root/.ssh inside the chosen service.
    Returns the override file path so the caller can pass it as a
    second `-f` flag to docker compose.

    Why an override file instead of editing the committed compose
    yaml: keeps the bind mount strictly per-profile and per-run, and
    docker-compose's natural -f chaining merges service.volumes lists
    without us having to round-trip yaml ourselves."""
    parent = stage_ssh.parent  # .slop/runtime/<profile>/
    override = parent / "docker-compose.override.yml"
    override.write_text(
        "# Generated by slop-orchestrator. Adds the staged ephemeral\n"
        "# SSH keys as a read-only bind mount so agent pushes from\n"
        "# inside the sandbox can resolve the github-llm-{ro,rw} aliases.\n"
        "services:\n"
        f"  {target_service}:\n"
        "    volumes:\n"
        f"      - {stage_ssh}:{CONTAINER_SSH_HOME}:ro\n"
    )
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
    override: Path | None = None
    if repo_root is not None and state_dir is not None and not dry_run:
        stage_ssh = _stage_github_credentials(profile, repo_root, state_dir)
        if stage_ssh is not None:
            override = _render_compose_override(stage_ssh)

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
        # Dry-run path mirrors the credential-gated branch by
        # describing what *would* be staged.
        gh = profile.credentials.get("github")
        if gh and gh != "none":
            print(
                "slop: would stage ephemeral GitHub keys at "
                "<state-dir>/runtime/<profile>/.ssh/ and bind-mount them "
                f"at {CONTAINER_SSH_HOME} in the agent-tools service."
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


def _launch_vm(profile: Profile, dry_run: bool = False) -> int:
    """Provision a disposable Tart VM and run the agent inside.

    Composes:
      slop-brew-vm init                # clone + boot the session VM
      slop-brew-vm run <agent>         # exec the agent inside

    The VM (`brew-sandbox-session` by default) is expected to have the
    agent pre-installed — typically via a one-time
    `slop-brew-vm install <agent>` against the trusted base template,
    or by baking it into the source image. The orchestrator does not
    auto-install: VMs are heavy and one-shot installs would surprise
    the user. If the agent isn't on PATH inside the VM, `slop-brew-vm
    run` fails with "command not found"; on-exit destroy-vm still
    runs to leave no stale state.

    `credentials.<host>` is created on the host as before (slop-gh-key
    here create-pair etc.); plumbing them into the VM is a follow-up
    sub-phase, not part of Phase G."""
    if profile.agent not in ("claude", "opencode"):
        raise OrchestratorError(
            f"agent {profile.agent!r} not yet supported on environment=vm. "
            "Pre-install your agent in the brew-vm trusted base template "
            "(`slop-brew-vm install <agent>`) and rerun."
        )
    cli_init = "slop-brew-vm init"
    cli_run = f"slop-brew-vm run {profile.agent}"
    print(f"slop: equivalent CLI: {cli_init} && {cli_run}")
    if dry_run:
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
    return _fish_exec(SLOP_BREW_VM, "run", profile.agent)


def _on_exit_hooks(profile: Profile, state: ProfileState) -> None:
    """Run profile.on_exit hooks idempotently. Errors print but don't
    abort the chain so partial cleanup still happens."""
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
            # Phase D: snapshot just leaves the existing state.json in
            # place; Phase E will copy it to .slop/snapshots/<utc-stamp>.json
            pass
        else:
            sys.stderr.write(f"slop: unknown on-exit hook {hook!r}; skipping\n")


def _revoke_credentials(state: ProfileState) -> None:
    if "github" in state.credentials:
        proc = _fish_run(SLOP_GH_KEY, "here", "cleanup")
        if proc.returncode != 0:
            sys.stderr.write(
                f"slop: gh-key cleanup failed ({proc.returncode}): {proc.stderr.rstrip()}\n"
            )
    if "forgejo" in state.credentials:
        proc = _fish_run(SLOP_FORGEJO_KEY, "here", "cleanup")
        if proc.returncode != 0:
            sys.stderr.write(
                f"slop: forgejo-key cleanup failed ({proc.returncode}): {proc.stderr.rstrip()}\n"
            )
    if "radicle" in state.credentials:
        proc = _fish_run(SLOP_RADICLE, "retire-expired", "--yes")
        if proc.returncode != 0:
            sys.stderr.write(
                f"slop: radicle retire-expired failed ({proc.returncode}): {proc.stderr.rstrip()}\n"
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
        rc = _launch_vm(profile)
    # Run on-exit hooks once the agent exits. Reload state so concurrent
    # runs in another tty don't lose each other's snapshots.
    state = _load_state(repo_root, cfg.state_dir)
    if profile.name in state:
        _on_exit_hooks(profile, state[profile.name])
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
        _on_exit_hooks(cfg.profiles[name], s)
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
