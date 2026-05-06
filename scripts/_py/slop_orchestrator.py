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


def _launch_host(profile: Profile) -> int:
    """Exec the agent REPL in the user's cwd. Composes slop-agents."""
    if profile.agent == "claude":
        return _fish_exec(SLOP_AGENTS, "claude")
    if profile.agent == "opencode":
        return _fish_exec(SLOP_AGENTS, "opencode")
    raise OrchestratorError(
        f"agent {profile.agent!r} not yet supported on environment=host. "
        f"Use environment=container in Phase E, or contribute a launcher."
    )


def _on_exit_hooks(profile: Profile, state: ProfileState) -> None:
    """Run profile.on_exit hooks idempotently. Errors print but don't
    abort the chain so partial cleanup still happens."""
    for hook in profile.on_exit:
        if hook == "revoke-credentials":
            _revoke_credentials(state)
        elif hook == "stop-container":
            sys.stderr.write(
                "slop: stop-container is a no-op for environment=host (Phase E adds it)\n"
            )
        elif hook == "stop-proxy":
            sys.stderr.write(
                "slop: stop-proxy is a no-op for environment=host (Phase E adds it)\n"
            )
        elif hook == "destroy-vm":
            sys.stderr.write(
                "slop: destroy-vm is a no-op for environment=host (Phase G adds it)\n"
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
    if profile.environment != "host":
        raise OrchestratorError(
            f"profile {profile.name!r} declares environment={profile.environment!r}; "
            "only `host` is supported in Phase D. Container/vm land in Phase E/G."
        )
    print(f"slop: launching profile {profile.name!r} (agent={profile.agent}, env=host)")
    cred_snapshot = _provision_credentials(profile)
    state = _load_state(repo_root, cfg.state_dir)
    state[profile.name] = ProfileState(
        started_at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        credentials=cred_snapshot,
    )
    _save_state(repo_root, cfg.state_dir, state)
    rc = _launch_host(profile)
    # Run on-exit hooks once the agent exits. Reload state so concurrent
    # runs in another tty don't lose each other's snapshots.
    state = _load_state(repo_root, cfg.state_dir)
    if profile.name in state:
        _on_exit_hooks(profile, state[profile.name])
        del state[profile.name]
        _save_state(repo_root, cfg.state_dir, state)
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
    p_run = sub.add_parser("run", help="Run a profile (host-only in this phase).")
    p_run.add_argument(
        "profile",
        nargs="?",
        default=None,
        help="Profile name. Omit to use `default`, or the only profile if there's one.",
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
