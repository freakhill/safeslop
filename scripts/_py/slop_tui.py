#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "textual>=0.79",
# ]
# ///
"""Slop — modern Textual TUI for the agentic_tactical_boots toolkit.

Why a Python rewrite:
- The previous fish/gum launcher hard-depended on `gum` and limited every
  screen to a flat `gum choose` list. This rewrite uses Textual so we get
  per-action single-key shortcuts, fuzzy filter, sub-menus as proper
  screens, an always-visible "Equivalent CLI" preview, a status footer,
  and full keyboard navigation (j/k, Enter, /, ?, q, Esc).

Architecture:
- One App with a stack of MenuScreens. Each screen renders a vertical
  ListView of Actions. Each Action carries a single-key shortcut, a label,
  a category, an `argv` list (for run-and-pause flows), and an optional
  `submenu` of children.
- Pressing the shortcut key, or Enter on a highlighted item, runs the
  action: either pushing a new screen for sub-menus, or suspending the
  app to run the subprocess in foreground. After a subprocess exits we
  pause for Enter so the user can read its output, then resume.
- The right-hand pane always shows the equivalent CLI for the highlighted
  action — the TUI is teachable, not opaque.

Running:
- `slop` (the fish wrapper) execs `uv run --script` against this file.
- PEP-723 means Textual is installed on first run; subsequent launches
  reuse uv's per-script cache and start in well under a second.
"""

from __future__ import annotations

import os
import shlex
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Iterable

from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical
from textual.reactive import reactive
from textual.screen import ModalScreen, Screen
from textual.widgets import (
    Button,
    Footer,
    Header,
    Input,
    Label,
    ListItem,
    ListView,
    Static,
)

REPO_ROOT = Path(__file__).resolve().parents[2]
SLOP_VERSION = "0.3"


# ---------------------------------------------------------------------------
# Action model.
# ---------------------------------------------------------------------------


@dataclass
class Action:
    """A single menu entry.

    An action either runs a subprocess (`argv` or `fish_tool`+`fish_sub`),
    or opens a sub-menu (`submenu`). Optional `prompts` collect free-text
    inputs before launching, and optional `confirm` shows a Yes/No modal.
    Inputs are interpolated into argv/fish_sub/equivalent_cli/confirm via
    `str.format(*inputs)`, so use `{0}`, `{1}`, … placeholders.

    All per-tool actions go through `fish_tool`+`fish_sub` so the underlying
    fish CLI subcommands are reused unchanged — the Textual layer only
    replaces the gum menus, not the actual implementation.
    """
    key: str
    label: str
    description: str = ""
    argv: list[str] | None = None
    fish_tool: str | None = None
    fish_sub: list[str] | None = None
    submenu: list["Action"] | None = None
    equivalent_cli: str | None = None
    prompts: list[tuple[str, str]] = field(default_factory=list)  # (placeholder, default)
    confirm: tuple[str, bool] | None = None  # (label format, default-yes)

    def resolved_argv(self, inputs: list[str]) -> list[str]:
        if self.fish_tool is not None and self.fish_sub is not None:
            sub = [s.format(*inputs) for s in self.fish_sub]
            return _fish_invocation(self.fish_tool, sub)
        if self.argv:
            return [a.format(*inputs) for a in self.argv]
        return []

    def resolved_cli(self, inputs: list[str]) -> str:
        if self.equivalent_cli is not None:
            return self.equivalent_cli.format(*inputs)
        if self.fish_tool is not None and self.fish_sub is not None:
            sub = " ".join(s.format(*inputs) for s in self.fish_sub)
            return f"{self.fish_tool} {sub}".rstrip()
        if self.argv:
            return " ".join(a.format(*inputs) for a in self.argv)
        if self.submenu:
            return f"(sub-menu: {len(self.submenu)} items)"
        return ""

    def resolved_confirm(self, inputs: list[str]) -> tuple[str, bool] | None:
        if self.confirm is None:
            return None
        return self.confirm[0].format(*inputs), self.confirm[1]

    @property
    def cli_text(self) -> str:
        # Preview-pane text. Shows raw {N} placeholders when prompts exist
        # because the user has not entered values yet.
        return self.resolved_cli([f"{{{i}}}" for i in range(len(self.prompts))])


def _fish_invocation(tool: str, sub_args: list[str]) -> list[str]:
    """Build a `fish -c 'source X; tool a b c'` argv with each interpolated
    arg shell-quoted. Sourcing the script first means the dispatcher
    function is in scope before invocation, matching how the conf.d shim
    resolves the tool when the user types it directly."""
    script = REPO_ROOT / "scripts" / f"{tool}.fish"
    quoted_sub = " ".join(shlex.quote(a) for a in sub_args)
    return ["fish", "-c", f"source {shlex.quote(str(script))}; {tool} {quoted_sub}"]


def _fish_script(name: str, *args: str) -> list[str]:
    """Run a fish script directly (for standalone scripts)."""
    script = REPO_ROOT / "scripts" / f"{name}.fish"
    return ["fish", str(script), *args]


# ---------------------------------------------------------------------------
# Per-tool action builders. Each returns a flat list of Actions that mirrors
# the previous fish/gum tui menu, but routes through the underlying non-
# interactive CLI subcommands instead of `slop-X tui`. Inputs use {0}, {1},
# … placeholders interpolated by Action.resolved_argv() at fire time.
# ---------------------------------------------------------------------------


def build_gh_key_actions() -> list[Action]:
    return [
        Action(
            key="c",
            label="Create RO+RW pair (24h, install ssh config)",
            fish_tool="slop-gh-key",
            fish_sub=["here", "create-pair"],
            equivalent_cli="slop-gh-key here create-pair",
            confirm=("Create RO+RW pair for the current repo?", True),
        ),
        Action(
            key="l",
            label="List current deploy keys",
            fish_tool="slop-gh-key",
            fish_sub=["here", "list"],
            equivalent_cli="slop-gh-key here list",
        ),
        Action(
            key="r",
            label="Revoke a key by id",
            fish_tool="slop-gh-key",
            fish_sub=["here", "revoke", "{0}"],
            equivalent_cli="slop-gh-key here revoke {0}",
            prompts=[("deploy key id (numeric, from the list)", "")],
            confirm=("Revoke key {0} from the current repo?", False),
        ),
        Action(
            key="R",
            label="Revoke ALL llm-agent keys for this repo",
            fish_tool="slop-gh-key",
            fish_sub=["here", "revoke-all"],
            equivalent_cli="slop-gh-key here revoke-all",
            confirm=("DESTRUCTIVE: revoke ALL 'llm-agent:' deploy keys?", False),
        ),
        Action(
            key="x",
            label="Cleanup expired keys",
            fish_tool="slop-gh-key",
            fish_sub=["here", "cleanup"],
            equivalent_cli="slop-gh-key here cleanup",
        ),
    ]


def build_forgejo_key_actions() -> list[Action]:
    return [
        Action(
            key="c",
            label="Create RO+RW pair (24h, install ssh config)",
            fish_tool="slop-forgejo-key",
            fish_sub=["here", "create-pair"],
            equivalent_cli="slop-forgejo-key here create-pair",
            confirm=("Create RO+RW pair for the current repo?", True),
        ),
        Action(
            key="l",
            label="List current deploy keys",
            fish_tool="slop-forgejo-key",
            fish_sub=["here", "list"],
            equivalent_cli="slop-forgejo-key here list",
        ),
        Action(
            key="r",
            label="Revoke a key by id",
            fish_tool="slop-forgejo-key",
            fish_sub=["here", "revoke", "{0}"],
            equivalent_cli="slop-forgejo-key here revoke {0}",
            prompts=[("deploy key id (numeric)", "")],
            confirm=("Revoke key {0} from the current repo?", False),
        ),
        Action(
            key="R",
            label="Revoke ALL llm-agent keys for this repo",
            fish_tool="slop-forgejo-key",
            fish_sub=["here", "revoke-all"],
            equivalent_cli="slop-forgejo-key here revoke-all",
            confirm=("DESTRUCTIVE: revoke ALL 'llm-agent:' deploy keys?", False),
        ),
        Action(
            key="x",
            label="Cleanup expired keys",
            fish_tool="slop-forgejo-key",
            fish_sub=["here", "cleanup"],
            equivalent_cli="slop-forgejo-key here cleanup",
        ),
    ]


def build_radicle_actions() -> list[Action]:
    return [
        Action(
            key="c",
            label="Create a new identity (24h TTL)",
            fish_tool="slop-radicle",
            fish_sub=["create-identity", "--name", "{0}", "--ttl", "24h"],
            equivalent_cli="slop-radicle create-identity --name {0} --ttl 24h",
            prompts=[("label (e.g. session-1)", "")],
            confirm=("Create identity '{0}'?", True),
        ),
        Action(
            key="l",
            label="List active identities",
            fish_tool="slop-radicle",
            fish_sub=["list-identities"],
            equivalent_cli="slop-radicle list-identities",
        ),
        Action(
            key="b",
            label="Bind THIS repo to an identity (read-only)",
            fish_tool="slop-radicle",
            fish_sub=["here", "bind", "--identity-id", "{0}", "--access", "ro"],
            equivalent_cli="slop-radicle here bind --identity-id {0} --access ro",
            prompts=[("identity id (from list)", "")],
            confirm=("Bind current repo to identity {0} (ro)?", True),
        ),
        Action(
            key="B",
            label="Bind THIS repo to an identity (read-write)",
            fish_tool="slop-radicle",
            fish_sub=["here", "bind", "--identity-id", "{0}", "--access", "rw"],
            equivalent_cli="slop-radicle here bind --identity-id {0} --access rw",
            prompts=[("identity id (from list)", "")],
            confirm=("Bind current repo to identity {0} (rw)?", True),
        ),
        Action(
            key="u",
            label="Unbind THIS repo (all identities)",
            fish_tool="slop-radicle",
            fish_sub=["here", "unbind", "--yes"],
            equivalent_cli="slop-radicle here unbind --yes",
            confirm=("Unbind current repo from ALL identities?", False),
        ),
        Action(
            key="L",
            label="List bindings for THIS repo",
            fish_tool="slop-radicle",
            fish_sub=["here", "list-bindings"],
            equivalent_cli="slop-radicle here list-bindings",
        ),
        Action(
            key="x",
            label="Retire expired identities",
            fish_tool="slop-radicle",
            fish_sub=["retire-expired", "--yes"],
            equivalent_cli="slop-radicle retire-expired --yes",
            confirm=("Retire all expired identities?", True),
        ),
    ]


def build_agent_sandbox_actions(tool: str) -> list[Action]:
    """Both slop-agent-sandbox and slop-agent-sandbox-tools share this menu;
    they only differ in which docker service the underlying CLI targets."""
    return [
        Action(
            key="u",
            label="Bring the stack up (build + proxy in background)",
            fish_tool=tool,
            fish_sub=["up"],
            equivalent_cli=f"{tool} up",
            confirm=("Build and start the stack?", True),
        ),
        Action(
            key="s",
            label="Open a one-shot shell in the container",
            fish_tool=tool,
            fish_sub=["shell"],
            equivalent_cli=f"{tool} shell",
            confirm=("Open a shell in the container?", True),
        ),
        Action(
            key="r",
            label="Run a one-off command in the container",
            fish_tool=tool,
            fish_sub=["run", "{0}"],
            equivalent_cli=f"{tool} run '{{0}}'",
            prompts=[("command (e.g. uv --version)", "")],
            confirm=("Run '{0}' in the container?", True),
        ),
        Action(
            key="d",
            label="Bring the stack down",
            fish_tool=tool,
            fish_sub=["down"],
            equivalent_cli=f"{tool} down",
            confirm=("Stop and remove the stack?", False),
        ),
    ]


def build_brew_vm_actions() -> list[Action]:
    return [
        Action(
            key="b",
            label="Create base template (one-time)",
            fish_tool="slop-brew-vm",
            fish_sub=["create-base"],
            equivalent_cli="slop-brew-vm create-base",
            confirm=("Run slop-brew-vm create-base now?", True),
        ),
        Action(
            key="i",
            label="Install a formula in disposable VM",
            fish_tool="slop-brew-vm",
            fish_sub=["install", "{0}"],
            equivalent_cli="slop-brew-vm install {0}",
            prompts=[("formula name (e.g. wget)", "")],
            confirm=("Install '{0}' in disposable VM?", True),
        ),
        Action(
            key="r",
            label="Run a one-off command in the session VM",
            fish_tool="slop-brew-vm",
            fish_sub=["run", "{0}"],
            equivalent_cli="slop-brew-vm run '{0}'",
            prompts=[("command (e.g. brew info wget)", "")],
            confirm=("Run '{0}' in VM?", True),
        ),
        Action(
            key="s",
            label="Open an SSH shell in the session VM",
            fish_tool="slop-brew-vm",
            fish_sub=["shell"],
            equivalent_cli="slop-brew-vm shell",
            confirm=("Open SSH shell in VM?", True),
        ),
        Action(
            key="v",
            label="Verify network policy enforcement",
            fish_tool="slop-brew-vm",
            fish_sub=["verify-network"],
            equivalent_cli="slop-brew-vm verify-network",
        ),
        Action(
            key="I",
            label="Copy a file INTO the VM",
            fish_tool="slop-brew-vm",
            fish_sub=["copy-in", "{0}", "{1}"],
            equivalent_cli="slop-brew-vm copy-in {0} {1}",
            prompts=[("host path", ""), ("guest path", "")],
            confirm=("Copy {0} → VM:{1} ?", True),
        ),
        Action(
            key="O",
            label="Copy a file OUT of the VM",
            fish_tool="slop-brew-vm",
            fish_sub=["copy-out", "{0}", "{1}"],
            equivalent_cli="slop-brew-vm copy-out {0} {1}",
            prompts=[("guest path", ""), ("host path", "")],
            confirm=("Copy VM:{0} → {1} ?", True),
        ),
        Action(
            key="x",
            label="Destroy the session VM",
            fish_tool="slop-brew-vm",
            fish_sub=["destroy"],
            equivalent_cli="slop-brew-vm destroy",
            confirm=("Really destroy the session VM?", False),
        ),
    ]


def build_agent_launcher_actions() -> list[Action]:
    """Launch Claude Code / OpenCode with the repo's bundled defaults
    applied. The fish helper resolves which settings file wins (cwd >
    repo root > user-level) and cd's there before exec'ing the agent
    binary, so the agent's own per-project precedence does the rest.
    Container-side launches stay under slop-agent-sandbox-tools — these
    actions are for the host."""
    return [
        Action(
            key="c",
            label="Launch Claude Code (apply defaults if no override)",
            fish_tool="slop-agents",
            fish_sub=["claude"],
            equivalent_cli="slop-agents claude",
        ),
        Action(
            key="o",
            label="Launch OpenCode (apply defaults if no override)",
            fish_tool="slop-agents",
            fish_sub=["opencode"],
            equivalent_cli="slop-agents opencode",
        ),
        Action(
            key="s",
            label="Seed default settings into repo root (claude + opencode)",
            fish_tool="slop-agents",
            fish_sub=["seed", "all"],
            equivalent_cli="slop-agents seed all",
            confirm=(
                "Write .claude/settings.json and opencode.json at the repo root?",
                True,
            ),
        ),
    ]


def build_top_actions() -> list[Action]:
    return [
        Action(
            key="a",
            label="Agents (Claude Code, OpenCode) — launch with defaults",
            description="Drop into Claude Code or OpenCode in the right cwd, with the repo's bundled defaults applied if no per-project override is present.",
            submenu=build_agent_launcher_actions(),
            equivalent_cli="(Agent launcher sub-menu)",
        ),
        Action(
            key="g",
            label="GitHub deploy keys (here = current repo)",
            description="Manage ephemeral RO/RW deploy keys for the current repo's git origin.",
            submenu=build_gh_key_actions(),
            equivalent_cli="(GitHub deploy-key sub-menu)",
        ),
        Action(
            key="f",
            label="Forgejo deploy keys",
            description="Multi-instance Forgejo deploy keys (Codeberg, self-hosted, etc.).",
            submenu=build_forgejo_key_actions(),
            equivalent_cli="(Forgejo deploy-key sub-menu)",
        ),
        Action(
            key="r",
            label="Radicle access",
            description="Per-repo Radicle identities and access policy.",
            submenu=build_radicle_actions(),
            equivalent_cli="(Radicle sub-menu)",
        ),
        Action(
            key="s",
            label="macOS local sandbox (sandbox-exec)",
            description="Defense-in-depth sandbox-exec runner. Print profile, run a command, open a sandboxed shell.",
            submenu=[
                Action(
                    key="p",
                    label="Print the profile that would be applied",
                    fish_tool="slop-macos-sandbox",
                    fish_sub=["print-profile"],
                    equivalent_cli="slop-macos-sandbox print-profile",
                ),
                Action(
                    key="r",
                    label="Run a one-off command in the sandbox",
                    fish_tool="slop-macos-sandbox",
                    # `--` separates flags from the command. The {0} is
                    # interpolated as a single shell-quoted argument by
                    # _fish_invocation, so commands with spaces ("ls -la")
                    # get passed through to /bin/sh -c via the wrapper.
                    fish_sub=["run", "--", "sh", "-c", "{0}"],
                    equivalent_cli="slop-macos-sandbox run -- sh -c '{0}'",
                    prompts=[("command (e.g. /bin/pwd or 'ls -la')", "")],
                    confirm=("Run '{0}' under sandbox-exec?", True),
                ),
                Action(
                    key="o",
                    label="Open a sandboxed /bin/zsh",
                    fish_tool="slop-macos-sandbox",
                    fish_sub=["shell"],
                    equivalent_cli="slop-macos-sandbox shell",
                ),
            ],
        ),
        Action(
            key="d",
            label="Docker agent stack",
            description="Build and run the minimal agent container behind the URL allowlist proxy.",
            submenu=build_agent_sandbox_actions("slop-agent-sandbox"),
            equivalent_cli="(Docker agent sub-menu)",
        ),
        Action(
            key="D",
            label="Docker agent + tools stack",
            description="Same stack with Claude Code / OpenCode / CrewAI / PydanticAI / AG2 preinstalled.",
            submenu=build_agent_sandbox_actions("slop-agent-sandbox-tools"),
            equivalent_cli="(Docker agent + tools sub-menu)",
        ),
        Action(
            key="b",
            label="Brew via disposable Tart VM",
            description="Evaluate suspicious Homebrew formulae inside a Tart macOS VM.",
            submenu=build_brew_vm_actions(),
            equivalent_cli="(Brew VM sub-menu)",
        ),
        Action(
            key="z",
            label="Unified isolation policy (slop-isolate)",
            description="Browse presets, validate a CUE config, start the proxy, tail recent denials. For compile/apply/approve, use the CLI directly.",
            submenu=[
                Action(
                    key="l",
                    label="List built-in presets",
                    fish_tool="slop-isolate",
                    fish_sub=["presets", "list"],
                    equivalent_cli="slop-isolate presets list",
                ),
                Action(
                    key="s",
                    label="Show a preset (claude-code)",
                    fish_tool="slop-isolate",
                    fish_sub=["presets", "show", "claude-code"],
                    equivalent_cli="slop-isolate presets show claude-code",
                ),
                Action(
                    key="v",
                    label="Validate examples/isolation/examples/user-config.cue",
                    fish_tool="slop-isolate",
                    fish_sub=[
                        "validate",
                        str(REPO_ROOT / "examples/isolation/examples/user-config.cue"),
                    ],
                    equivalent_cli="slop-isolate validate examples/isolation/examples/user-config.cue",
                ),
                Action(
                    key="p",
                    label="Start the Envoy + CoreDNS + notifier proxy",
                    fish_tool="slop-isolate",
                    fish_sub=["proxy", "start"],
                    equivalent_cli="slop-isolate proxy start",
                ),
                Action(
                    key="d",
                    label="Tail recent denials",
                    fish_tool="slop-isolate",
                    fish_sub=["denials", "--since", "1h"],
                    equivalent_cli="slop-isolate denials --since 1h",
                ),
            ],
        ),
        Action(
            key="i",
            label="Install / uninstall fish-tool shims",
            description="Manage ~/.config/fish/conf.d/agentic_tactical_boots.fish.",
            submenu=[
                Action(
                    key="i",
                    label="Install (idempotent)",
                    argv=_fish_script("slop-install", "install"),
                    equivalent_cli="scripts/slop-install.fish install",
                ),
                Action(
                    key="u",
                    label="Uninstall",
                    argv=_fish_script("slop-install", "uninstall"),
                    equivalent_cli="scripts/slop-install.fish uninstall",
                ),
                Action(
                    key="s",
                    label="Status",
                    argv=_fish_script("slop-install", "status"),
                    equivalent_cli="scripts/slop-install.fish status",
                ),
            ],
        ),
        Action(
            key="k",
            label="Install local skills",
            description="Copy repo's skills/ directories into ~/.claude/skills/.",
            submenu=[
                Action(
                    key="i",
                    label="Install (skip existing)",
                    argv=_fish_script("slop-skills-install"),
                    equivalent_cli="scripts/slop-skills-install.fish",
                ),
                Action(
                    key="f",
                    label="Install --force (replace existing)",
                    argv=_fish_script("slop-skills-install", "--force"),
                    equivalent_cli="scripts/slop-skills-install.fish --force",
                ),
                Action(
                    key="d",
                    label="Preview --dry-run",
                    argv=_fish_script("slop-skills-install", "--dry-run"),
                    equivalent_cli="scripts/slop-skills-install.fish --dry-run",
                ),
            ],
        ),
        Action(
            key="v",
            label="Verifications (pinning, help-sync, tests)",
            description="Run the maintenance gates the repo expects in CI.",
            submenu=[
                Action(
                    key="p",
                    label="Run pinning check",
                    argv=_fish_script("slop-pinning"),
                    equivalent_cli="scripts/slop-pinning.fish",
                ),
                Action(
                    key="h",
                    label="Run help-text sync check",
                    argv=_fish_script("slop-sync-help", "check"),
                    equivalent_cli="scripts/slop-sync-help.fish check",
                ),
                Action(
                    key="t",
                    label="Run full test suite",
                    argv=["fish", str(REPO_ROOT / "tests" / "run.fish")],
                    equivalent_cli="fish tests/run.fish",
                ),
            ],
        ),
        Action(
            key="R",
            label="Show README quickstart",
            description="Render the first ~60 lines of README.md inline.",
            argv=["sh", "-c", f"sed -n '1,60p' '{REPO_ROOT / 'README.md'}'"],
            equivalent_cli="head -n 60 README.md",
        ),
    ]


# ---------------------------------------------------------------------------
# Status helpers.
# ---------------------------------------------------------------------------


def _git_origin(cwd: Path) -> str:
    try:
        out = subprocess.check_output(
            ["git", "-C", str(cwd), "remote", "get-url", "origin"],
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
        return out or "(no git origin)"
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "(no git origin)"


def _dep_status() -> str:
    parts = []
    for tool in ("uv", "cue", "fish", "gh", "tart", "docker", "ollama"):
        ok = shutil.which(tool) is not None
        parts.append(f"{tool}={'ok' if ok else 'missing'}")
    return "  ".join(parts)


# ---------------------------------------------------------------------------
# Screens.
# ---------------------------------------------------------------------------


class MenuScreen(Screen):
    """A single menu screen. Push a new instance to drill into a sub-menu."""

    BINDINGS = [
        Binding("escape", "back", "Back", priority=True),
        Binding("q", "quit", "Quit"),
        Binding("?", "help", "Help"),
        Binding("/", "filter", "Filter"),
        Binding("j", "cursor_down", "Down", show=False),
        Binding("k", "cursor_up", "Up", show=False),
    ]

    DEFAULT_CSS = """
    MenuScreen {
        layout: vertical;
    }
    #context {
        height: 3;
        padding: 0 1;
        color: $text-muted;
    }
    #body {
        height: 1fr;
    }
    #list {
        width: 60%;
        border: round $primary;
        margin: 0 1 0 1;
    }
    #preview {
        width: 40%;
        border: round $accent;
        padding: 0 1;
        margin: 0 1 0 0;
    }
    #help-overlay {
        layer: above;
        align: center middle;
        background: $surface;
        border: heavy $accent;
        padding: 1 2;
        max-width: 70;
    }
    """

    actions: list[Action]
    title_text: str
    # init=False: skip the initial watcher fire. The reactive defaults to ""
    # and Textual would otherwise call watch_filter_text() before compose()
    # runs, when #list doesn't exist yet — raising NoMatches and crashing
    # the app on launch. compose() reads filter_text directly, so the
    # initial value is reflected without needing the watcher to fire.
    filter_text: reactive[str] = reactive("", init=False)

    def __init__(self, actions: list[Action], title: str) -> None:
        super().__init__()
        self.actions = actions
        self.title_text = title

    def compose(self) -> ComposeResult:
        yield Header(show_clock=False)
        yield Static(self._context_text(), id="context")
        with Horizontal(id="body"):
            yield ListView(*self._list_items(), id="list")
            yield Static("", id="preview")
        yield Footer()

    def _context_text(self) -> str:
        cwd = Path(os.environ.get("ATB_USER_PWD", os.getcwd()))
        return (
            f"slop v{SLOP_VERSION} — {self.title_text}\n"
            f"cwd: {cwd}    origin: {_git_origin(cwd)}\n"
            f"deps: {_dep_status()}"
        )

    def _list_items(self) -> list[ListItem]:
        items = []
        f = self.filter_text.lower().strip()
        for a in self.actions:
            if f and f not in a.label.lower() and f not in a.description.lower():
                continue
            label_text = f"[b]{a.key}[/]  {a.label}"
            items.append(ListItem(Label(label_text), id=f"act-{id(a)}"))
        return items

    def on_mount(self) -> None:
        self.title = "slop"
        self.sub_title = self.title_text
        lv = self.query_one("#list", ListView)
        lv.focus()
        self._refresh_preview()

    def watch_filter_text(self, _old: str, _new: str) -> None:
        lv = self.query_one("#list", ListView)
        lv.clear()
        for li in self._list_items():
            lv.append(li)
        self._refresh_preview()

    def _current_action(self) -> Action | None:
        lv = self.query_one("#list", ListView)
        idx = lv.index
        if idx is None:
            return None
        visible = [a for a in self.actions
                   if not self.filter_text
                   or self.filter_text.lower() in a.label.lower()
                   or self.filter_text.lower() in a.description.lower()]
        if 0 <= idx < len(visible):
            return visible[idx]
        return None

    def _refresh_preview(self) -> None:
        a = self._current_action()
        preview = self.query_one("#preview", Static)
        if a is None:
            preview.update("(no selection)")
            return
        body = []
        body.append(f"[b]{a.label}[/]")
        if a.description:
            body.append("")
            body.append(a.description)
        body.append("")
        body.append("[b]Equivalent CLI:[/]")
        body.append(f"  {a.cli_text}")
        if a.submenu:
            body.append("")
            body.append("[i]press Enter or its key to open the sub-menu[/]")
        preview.update("\n".join(body))

    def on_list_view_highlighted(self, event: ListView.Highlighted) -> None:  # noqa: ARG002
        self._refresh_preview()

    def on_list_view_selected(self, event: ListView.Selected) -> None:  # noqa: ARG002
        self._fire(self._current_action())

    def on_key(self, event) -> None:
        # Single-key shortcut dispatch. Only fires when the user types a key
        # that matches an action's `key` field (case-sensitive so g and G can
        # differ — Docker agent vs Docker agent + tools).
        if event.character is None:
            return
        for a in self.actions:
            if event.character == a.key:
                event.stop()
                self._fire(a)
                return

    def _fire(self, action: Action | None) -> None:
        if action is None:
            return
        if action.submenu:
            self.app.push_screen(MenuScreen(action.submenu, action.label))
            return
        # Sequence: collect each prompt → optional confirm → run subprocess.
        # Each step is callback-driven so the user can cancel at any modal
        # without leaking through to the next step.
        self._collect_inputs(action, [])

    def _collect_inputs(self, action: Action, collected: list[str]) -> None:
        if len(collected) < len(action.prompts):
            placeholder, default = action.prompts[len(collected)]

            def _on_input(value: str | None) -> None:
                if value is None:
                    return
                self._collect_inputs(action, collected + [value])

            self.app.push_screen(
                InputScreen(action.label, placeholder, default), _on_input
            )
            return
        self._maybe_confirm(action, collected)

    def _maybe_confirm(self, action: Action, collected: list[str]) -> None:
        rc = action.resolved_confirm(collected)
        if rc is None:
            self._do_run(action, collected)
            return
        prompt, default_yes = rc

        def _on_confirm(yes: bool | None) -> None:
            if not yes:
                return
            self._do_run(action, collected)

        self.app.push_screen(ConfirmScreen(prompt, default_yes), _on_confirm)

    def _do_run(self, action: Action, collected: list[str]) -> None:
        if not (action.argv or (action.fish_tool and action.fish_sub)):
            return
        self.app.run_subprocess(action, collected)

    def action_back(self) -> None:
        if len(self.app.screen_stack) > 1:
            self.app.pop_screen()
        else:
            self.app.exit()

    def action_quit(self) -> None:
        self.app.exit()

    def action_filter(self) -> None:
        self.app.push_screen(FilterScreen(self))

    def action_help(self) -> None:
        self.app.push_screen(HelpScreen())


class FilterScreen(Screen):
    """Modal filter input. Live-filters the parent menu as the user types."""

    BINDINGS = [
        Binding("escape", "cancel", "Cancel", priority=True),
        Binding("enter", "submit", "Apply"),
    ]

    DEFAULT_CSS = """
    FilterScreen {
        align: center middle;
    }
    #filter-box {
        width: 60;
        border: round $accent;
        padding: 0 1;
        background: $surface;
    }
    """

    def __init__(self, parent_screen: MenuScreen) -> None:
        super().__init__()
        self.parent_screen = parent_screen

    def compose(self) -> ComposeResult:
        with Vertical(id="filter-box"):
            yield Label("Filter (Esc cancel, Enter apply)")
            yield Input(placeholder="type to filter…", value=self.parent_screen.filter_text)

    def on_mount(self) -> None:
        self.query_one(Input).focus()

    def on_input_changed(self, event: Input.Changed) -> None:
        self.parent_screen.filter_text = event.value

    def action_submit(self) -> None:
        self.app.pop_screen()

    def action_cancel(self) -> None:
        self.parent_screen.filter_text = ""
        self.app.pop_screen()


class InputScreen(ModalScreen[str]):
    """Single-line text input modal. Replaces the old `gum input` prompts.
    Dismisses with the entered string on Enter, or `None` on Esc.
    """

    BINDINGS = [
        Binding("escape", "cancel", "Cancel", priority=True),
        Binding("enter", "submit", "Submit"),
    ]

    DEFAULT_CSS = """
    InputScreen {
        align: center middle;
    }
    #input-box {
        width: 70;
        border: heavy $accent;
        padding: 1 2;
        background: $surface;
        height: auto;
    }
    """

    def __init__(self, title: str, placeholder: str, default: str = "") -> None:
        super().__init__()
        self._title = title
        self._placeholder = placeholder
        self._default = default

    def compose(self) -> ComposeResult:
        with Vertical(id="input-box"):
            yield Label(f"[b]{self._title}[/]")
            yield Label(f"[i]{self._placeholder}[/]")
            yield Input(placeholder=self._placeholder, value=self._default, id="input-field")
            yield Label("[dim]Enter to submit · Esc to cancel[/]")

    def on_mount(self) -> None:
        self.query_one("#input-field", Input).focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        self.dismiss(event.value)

    def action_submit(self) -> None:
        self.dismiss(self.query_one("#input-field", Input).value)

    def action_cancel(self) -> None:
        self.dismiss(None)


class ConfirmScreen(ModalScreen[bool]):
    """Yes/No modal. Replaces the old `gum confirm` prompts. Dismisses with
    True on Yes, False on No, None on Esc. `default_yes` controls which
    button has initial focus and which fires on Enter.
    """

    BINDINGS = [
        Binding("escape", "cancel", "Cancel", priority=True),
        Binding("y", "yes", "Yes", show=False),
        Binding("n", "no", "No", show=False),
        Binding("enter", "default", "Default", show=False),
    ]

    DEFAULT_CSS = """
    ConfirmScreen {
        align: center middle;
    }
    #confirm-box {
        width: 70;
        border: heavy $warning;
        padding: 1 2;
        background: $surface;
        height: auto;
    }
    #buttons {
        height: auto;
        align: center middle;
        margin-top: 1;
    }
    Button {
        margin: 0 1;
    }
    """

    def __init__(self, prompt: str, default_yes: bool = True) -> None:
        super().__init__()
        self._prompt = prompt
        self._default_yes = default_yes

    def compose(self) -> ComposeResult:
        with Vertical(id="confirm-box"):
            yield Label(f"[b]{self._prompt}[/]")
            yield Label("")
            with Horizontal(id="buttons"):
                yield Button(
                    "Yes",
                    id="yes",
                    variant="success" if self._default_yes else "default",
                )
                yield Button(
                    "No",
                    id="no",
                    variant="error" if not self._default_yes else "default",
                )
            yield Label(
                "[dim]y/n · Enter for default ("
                + ("Yes" if self._default_yes else "No")
                + ") · Esc to cancel[/]"
            )

    def on_mount(self) -> None:
        target = "yes" if self._default_yes else "no"
        self.query_one(f"#{target}", Button).focus()

    def on_button_pressed(self, event: Button.Pressed) -> None:
        self.dismiss(event.button.id == "yes")

    def action_yes(self) -> None:
        self.dismiss(True)

    def action_no(self) -> None:
        self.dismiss(False)

    def action_default(self) -> None:
        self.dismiss(self._default_yes)

    def action_cancel(self) -> None:
        self.dismiss(None)


class HelpScreen(Screen):
    """Keyboard reference overlay."""

    BINDINGS = [
        Binding("escape", "close", "Close", priority=True),
        Binding("q", "close", "Close"),
        Binding("?", "close", "Close"),
    ]

    DEFAULT_CSS = """
    HelpScreen {
        align: center middle;
    }
    #help-body {
        width: 70;
        border: heavy $accent;
        padding: 1 2;
        background: $surface;
    }
    """

    def compose(self) -> ComposeResult:
        with Vertical(id="help-body"):
            yield Label("[b]slop — keyboard reference[/]")
            yield Label("")
            yield Label("Navigation:")
            yield Label("  ↑/↓  or  k/j      Move selection")
            yield Label("  Enter             Run selected action / open sub-menu")
            yield Label("  Esc               Back / quit")
            yield Label("  q                 Quit")
            yield Label("")
            yield Label("Discovery:")
            yield Label("  /                 Filter the visible list")
            yield Label("  ?                 Toggle this help")
            yield Label("")
            yield Label("Action shortcuts:")
            yield Label("  Each row's leading letter runs that action immediately.")
            yield Label("")
            yield Label("Equivalent CLI:")
            yield Label("  Always shown in the right panel — copy and use it directly.")
            yield Label("")
            yield Label("[i]Press Esc, q, or ? to close.[/]")

    def action_close(self) -> None:
        self.app.pop_screen()


# ---------------------------------------------------------------------------
# App.
# ---------------------------------------------------------------------------


class SlopApp(App):
    TITLE = "slop"
    SUB_TITLE = "agentic_tactical_boots launcher"

    BINDINGS = [
        Binding("ctrl+c", "quit", "Quit", show=False),
    ]

    def on_mount(self) -> None:
        self.push_screen(MenuScreen(build_top_actions(), "main menu"))

    def run_subprocess(self, action: Action, inputs: list[str] | None = None) -> None:
        """Suspend Textual, run the action's resolved argv in foreground,
        prompt to continue, then resume. Suspending hands the terminal back
        to the subprocess so the underlying CLI subcommands inherit a real
        TTY for any output they paginate.

        Surfaces a non-zero exit explicitly: the previous version silently
        returned to the menu on failure, which made silent gh/CLI errors
        (e.g. `gh api` 4xx responses) look like "the action did nothing"
        because the menu just reappeared."""
        inputs = inputs or []
        argv = action.resolved_argv(inputs)
        if not argv:
            return
        cli = action.resolved_cli(inputs)
        with self.suspend():
            print()
            print("Equivalent CLI:")
            print(f"  {cli}")
            print()
            rc = 0
            try:
                rc = _spawn_with_ctty(argv)
            except KeyboardInterrupt:
                rc = 130
            print()
            if rc != 0:
                print(f"⚠ exit code: {rc}")
                print()
            try:
                input("Press Enter to return to slop… ")
            except (EOFError, KeyboardInterrupt):
                pass


def _spawn_with_ctty(argv: list[str]) -> int:
    """Fork a child, place it in its own process group, hand it the
    foreground of the controlling terminal, exec argv, wait, and return
    the child's exit status.

    Why we don't just use `subprocess.call`: when an interactive child
    (zsh, vi, less) tries to claim the terminal foreground with
    `tcsetpgrp(stdin, getpgrp())`, the kernel returns EPERM unless the
    child is already a process-group leader of its own pgrp. Plain
    subprocess.call inherits the parent's pgrp, so on macOS zsh prints
    `can't set tty pgrp: operation not permitted` and immediately drops
    back. The fix mirrors what shells do for foreground children:
    setpgid the child into its own group, tcsetpgrp that group to
    foreground, exec. Parent restores the saved foreground pgrp on the
    way out so Textual can resume cleanly.

    SIGTTOU is ignored during the parent's tcsetpgrp because the parent
    is no longer in the foreground pgrp by the time we restore — the
    write would otherwise stop the parent.

    No setsid: a fresh session would lose the ctty, and re-acquiring it
    via /dev/tty fails with ENXIO on macOS the moment the session leader
    has no ctty. Staying in the same session keeps the terminal usable
    for both interactive (zsh) and non-interactive (gh api) children.

    Falls back to subprocess.call on platforms without fork (Windows).
    """
    if not hasattr(os, "fork"):
        return subprocess.call(argv)
    import signal
    try:
        saved_pgrp: int | None = os.tcgetpgrp(0)
    except OSError:
        saved_pgrp = None
    saved_sigttou = signal.signal(signal.SIGTTOU, signal.SIG_IGN)
    try:
        pid = os.fork()
        if pid == 0:
            try:
                os.setpgid(0, 0)
                if saved_pgrp is not None:
                    try:
                        os.tcsetpgrp(0, os.getpgrp())
                    except OSError:
                        pass
                # Reset SIGTTOU in the child so the exec'd program sees
                # default handling. (signal.signal in the child does not
                # affect the parent.)
                signal.signal(signal.SIGTTOU, signal.SIG_DFL)
                os.execvp(argv[0], argv)
            except Exception as e:
                sys.stderr.write(f"slop: exec failed: {e!r}\n")
                os._exit(127)
        # Parent also does setpgid to avoid a race where the child's
        # setpgid hasn't run by the time we tcsetpgrp from outside.
        try:
            os.setpgid(pid, pid)
        except OSError:
            pass
        _, status = os.waitpid(pid, 0)
        if saved_pgrp is not None:
            try:
                os.tcsetpgrp(0, saved_pgrp)
            except OSError:
                pass
    finally:
        signal.signal(signal.SIGTTOU, saved_sigttou)
    if os.WIFEXITED(status):
        return os.WEXITSTATUS(status)
    if os.WIFSIGNALED(status):
        return 128 + os.WTERMSIG(status)
    return 1


def _walk_actions(actions: list[Action]) -> Iterable[tuple[list[str], Action]]:
    """Yield (path, action) for every leaf action in the tree. Path is the
    chain of parent labels leading to the leaf, useful for error messages."""
    def _walk(actions: list[Action], path: list[str]):
        for a in actions:
            here = path + [a.label]
            if a.submenu:
                yield from _walk(a.submenu, here)
            else:
                yield here, a
    yield from _walk(actions, [])


def _audit_actions() -> int:
    """Walk the entire top-level action tree and report any leaf that:
      - still shells out to a legacy `slop-X tui` flow (the rewrite was
        meant to replace those with native Textual sub-menus), or
      - has no runnable argv at all, or
      - has placeholder `{N}` tokens left in resolved argv after dummy
        inputs are substituted (means the prompts list is too short), or
      - passes a positional verb to a fish script that the script's
        dispatcher does not recognize (would yield "Unknown argument" /
        "unknown command" at runtime).
    Prints one ` FAIL <label>: <reason>` line per problem and returns
    non-zero if any were found. Otherwise prints an `OK <count> leaf
    actions` summary and returns 0."""
    import re
    failures: list[str] = []
    leaves = list(_walk_actions(build_top_actions()))
    for path, action in leaves:
        label = " > ".join(path)
        # Substitute dummy inputs so resolved_* never returns a {N}
        # placeholder. Use distinguishable values to also catch ordering bugs.
        dummies = [f"_in{i}_" for i in range(len(action.prompts))]
        argv = action.resolved_argv(dummies)
        cli = action.resolved_cli(dummies)
        if not argv:
            failures.append(f"{label}: no argv (neither argv nor fish_tool+fish_sub set)")
            continue
        # Look for a tail-`tui` invocation: the legacy entry-point pattern
        # that pre-dates the Textual rewrite. fish_tool is the canonical
        # way the launcher invokes per-tool scripts; if its sub_args end
        # in `tui` we are still routing through the gum menu.
        if action.fish_tool and action.fish_sub and action.fish_sub == ["tui"]:
            failures.append(f"{label}: still calls `{action.fish_tool} tui`")
        if " tui" in cli and cli.strip().endswith(" tui"):
            failures.append(f"{label}: equivalent_cli still ends in ' tui' ({cli!r})")
        # Catch leftover {N} placeholders.
        joined = " ".join(argv)
        if "{0}" in joined or "{1}" in joined or "{2}" in joined:
            failures.append(
                f"{label}: resolved_argv left placeholder unfilled "
                f"(prompts={len(action.prompts)}, argv={argv!r})"
            )
        # Verb-acceptance check: every action that passes a bare-word
        # positional verb to a fish script's dispatcher must use a verb
        # that the script's `case` statement actually handles. Catches
        # the class of bug where the TUI hallucinates a subcommand
        # (e.g. `slop-skills-install install` when the script only takes
        # flags). Two argv shapes to handle:
        #   1) Direct fish-script: ["fish", "<X.fish>", "<verb>", ...]
        #   2) Sourced fish-tool:  ["fish", "-c", "source X; tool <verb> ..."]
        verb_to_check: str | None = None
        script_path: Path | None = None
        if (
            len(argv) >= 3
            and argv[0] == "fish"
            and argv[1].endswith(".fish")
            and not argv[2].startswith("-")
            and "{" not in argv[2]
        ):
            verb_to_check = argv[2]
            script_path = Path(argv[1])
        elif (
            action.fish_tool is not None
            and action.fish_sub
            and not action.fish_sub[0].startswith("-")
            and "{" not in action.fish_sub[0]
        ):
            verb_to_check = action.fish_sub[0]
            script_path = REPO_ROOT / "scripts" / f"{action.fish_tool}.fish"
        if verb_to_check is not None and script_path is not None:
            try:
                src = script_path.read_text()
            except OSError:
                src = ""
            # Two dispatcher shapes to recognize:
            #   (1) `case <verb>` inside a `switch "$cmd"` block. Multi-
            #       word `case foo bar baz` is normal in fish; strip
            #       quotes and split per line for membership.
            #   (2) `test "$cmd" = "<verb>"` (and variants like
            #       `$argv[1] = "<verb>"`). slop-gh-key dispatches `here`
            #       and `tui` this way, before the main switch.
            handled = False
            for line in src.splitlines():
                m = re.match(r"\s*case\s+(.+?)\s*$", line)
                if m:
                    words = []
                    for w in m.group(1).split():
                        w = w.strip("'\"")
                        if w and w != "*":
                            words.append(w)
                    if verb_to_check in words:
                        handled = True
                        break
            if not handled and src:
                # Comparison form: `= "<verb>"` (or `= '<verb>'`).
                cmp_pat = rf"=\s*['\"]{re.escape(verb_to_check)}['\"]"
                if re.search(cmp_pat, src):
                    handled = True
            if src and not handled:
                failures.append(
                    f"{label}: verb {verb_to_check!r} not handled by any "
                    f"`case` or `=` comparison in {script_path.name} — "
                    f"would fail with 'Unknown argument'"
                )
    if failures:
        for f in failures:
            print(f"FAIL {f}", file=sys.stderr)
        return 1
    print(f"OK {len(leaves)} leaf actions")
    return 0


async def _mount_check() -> None:
    """Drive the App through compose+mount under Textual's headless test
    driver. Surfaces any error that would crash a real launch (e.g. a
    reactive watcher firing before compose creates the widget it queries).
    Drills into a per-tool sub-menu and triggers an action with prompts so
    the FilterScreen, MenuScreen, InputScreen, and ConfirmScreen mount
    paths are all exercised."""
    app = SlopApp()
    async with app.run_test() as pilot:
        await pilot.pause()
        # Filter screen
        await pilot.press("/")
        await pilot.pause()
        await pilot.press("escape")
        await pilot.pause()
        # Drill into the GitHub deploy-key sub-menu.
        await pilot.press("g")
        await pilot.pause()
        # 'r' = Revoke a key by id → has a prompt, opens InputScreen.
        await pilot.press("r")
        await pilot.pause()
        # Cancel the input.
        await pilot.press("escape")
        await pilot.pause()
        # Pop back to top menu.
        await pilot.press("escape")
        await pilot.pause()
        # Drill into Brew VM sub-menu (multi-prompt actions live here).
        await pilot.press("b")
        await pilot.pause()
        await pilot.press("escape")
        await pilot.pause()


def main(argv: list[str] | None = None) -> int:
    args = list(argv) if argv is not None else sys.argv[1:]
    if "--self-check" in args:
        # `slop --check` exercises this path. Reaching it means uv resolved
        # the PEP-723 deps and Textual imported successfully — the actual
        # work the self-check is meant to confirm. We do not start the App.
        print("slop_tui: textual import OK")
        return 0
    if "--mount-check" in args:
        import asyncio
        try:
            asyncio.run(_mount_check())
        except Exception as e:
            print(f"slop_tui: mount-check FAILED: {e!r}", file=sys.stderr)
            return 1
        print("slop_tui: mount-check OK")
        return 0
    if "--audit" in args:
        return _audit_actions()
    SlopApp().run()
    return 0


if __name__ == "__main__":
    sys.exit(main())
