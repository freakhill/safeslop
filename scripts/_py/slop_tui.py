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
from textual.screen import Screen
from textual.widgets import Footer, Header, Input, Label, ListItem, ListView, Static

REPO_ROOT = Path(__file__).resolve().parents[2]
SLOP_VERSION = "0.2"


# ---------------------------------------------------------------------------
# Action model.
# ---------------------------------------------------------------------------


@dataclass
class Action:
    """A single menu entry.

    Either `argv` (run a subprocess) or `submenu` (push a new screen) must
    be set. `equivalent_cli` is shown verbatim in the preview pane so the
    user can copy it; it defaults to the argv joined with spaces.
    """
    key: str
    label: str
    description: str = ""
    argv: list[str] | None = None
    submenu: list["Action"] | None = None
    equivalent_cli: str | None = None
    needs_input: tuple[str, str] | None = None  # (placeholder, env-var) for one-shot prompt

    @property
    def cli_text(self) -> str:
        if self.equivalent_cli is not None:
            return self.equivalent_cli
        if self.argv:
            return " ".join(self.argv)
        if self.submenu:
            return f"(sub-menu: {len(self.submenu)} items)"
        return ""


def _fish_tool(name: str, sub: str = "tui") -> list[str]:
    """Run a per-tool fish TUI. Source the script then call its function so
    the function definitions are in scope before invocation."""
    script = REPO_ROOT / "scripts" / f"{name}.fish"
    return ["fish", "-c", f"source '{script}'; {name} {sub}"]


def _fish_script(name: str, *args: str) -> list[str]:
    """Run a fish script directly (for standalone scripts)."""
    script = REPO_ROOT / "scripts" / f"{name}.fish"
    return ["fish", str(script), *args]


def build_top_actions() -> list[Action]:
    return [
        Action(
            key="g",
            label="GitHub deploy keys (here = current repo)",
            description="Manage ephemeral RO/RW deploy keys for the current repo's git origin.",
            argv=_fish_tool("slop-gh-key"),
            equivalent_cli="slop-gh-key tui",
        ),
        Action(
            key="f",
            label="Forgejo deploy keys",
            description="Multi-instance Forgejo deploy keys (Codeberg, self-hosted, etc.).",
            argv=_fish_tool("slop-forgejo-key"),
            equivalent_cli="slop-forgejo-key tui",
        ),
        Action(
            key="r",
            label="Radicle access",
            description="Per-repo Radicle identities and access policy.",
            argv=_fish_tool("slop-radicle"),
            equivalent_cli="slop-radicle tui",
        ),
        Action(
            key="s",
            label="macOS local sandbox (sandbox-exec)",
            description="Defense-in-depth sandbox-exec runner. Print profile, run a command, open a sandboxed shell.",
            submenu=[
                Action(
                    key="p",
                    label="Print the profile that would be applied",
                    argv=["fish", "-c",
                          f"source '{REPO_ROOT}/scripts/slop-macos-sandbox.fish'; slop-macos-sandbox print-profile"],
                    equivalent_cli="slop-macos-sandbox print-profile",
                ),
                Action(
                    key="o",
                    label="Open a sandboxed /bin/zsh",
                    argv=["fish", "-c",
                          f"source '{REPO_ROOT}/scripts/slop-macos-sandbox.fish'; slop-macos-sandbox shell"],
                    equivalent_cli="slop-macos-sandbox shell",
                ),
            ],
        ),
        Action(
            key="d",
            label="Docker agent stack",
            description="Build and run the minimal agent container behind the URL allowlist proxy.",
            argv=_fish_tool("slop-agent-sandbox"),
            equivalent_cli="slop-agent-sandbox tui",
        ),
        Action(
            key="D",
            label="Docker agent + tools stack",
            description="Same stack with Claude Code / OpenCode / CrewAI / PydanticAI / AG2 preinstalled.",
            argv=_fish_tool("slop-agent-sandbox-tools"),
            equivalent_cli="slop-agent-sandbox-tools tui",
        ),
        Action(
            key="b",
            label="Brew via disposable Tart VM",
            description="Evaluate suspicious Homebrew formulae inside a Tart macOS VM.",
            argv=_fish_tool("slop-brew-vm"),
            equivalent_cli="slop-brew-vm tui",
        ),
        Action(
            key="z",
            label="Unified isolation policy (slop-isolate)",
            description="Compile a CUE isolation config to per-tool outputs; manage the proxy + approve flow.",
            submenu=[
                Action(
                    key="l",
                    label="List built-in presets",
                    argv=_fish_tool("slop-isolate", "presets list"),
                    equivalent_cli="slop-isolate presets list",
                ),
                Action(
                    key="s",
                    label="Show a preset (claude-code)",
                    argv=_fish_tool("slop-isolate", "presets show claude-code"),
                    equivalent_cli="slop-isolate presets show claude-code",
                ),
                Action(
                    key="v",
                    label="Validate examples/isolation/examples/user-config.cue",
                    argv=_fish_tool(
                        "slop-isolate",
                        f"validate '{REPO_ROOT}/examples/isolation/examples/user-config.cue'",
                    ),
                    equivalent_cli="slop-isolate validate examples/isolation/examples/user-config.cue",
                ),
                Action(
                    key="p",
                    label="Start the Envoy + CoreDNS + notifier proxy",
                    argv=_fish_tool("slop-isolate", "proxy start"),
                    equivalent_cli="slop-isolate proxy start",
                ),
                Action(
                    key="d",
                    label="Tail recent denials",
                    argv=_fish_tool("slop-isolate", "denials --since 1h"),
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
            label="Install / uninstall local skills",
            description="Wire repo-local skills into Claude / OpenCode.",
            submenu=[
                Action(
                    key="i",
                    label="Install local skills",
                    argv=_fish_script("slop-skills-install", "install"),
                    equivalent_cli="scripts/slop-skills-install.fish install",
                ),
                Action(
                    key="u",
                    label="Uninstall local skills",
                    argv=_fish_script("slop-skills-install", "uninstall"),
                    equivalent_cli="scripts/slop-skills-install.fish uninstall",
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
    filter_text: reactive[str] = reactive("")

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
        if action.argv:
            self.app.run_subprocess(action)

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

    def run_subprocess(self, action: Action) -> None:
        """Suspend Textual, run the action's argv in foreground, prompt to
        continue, then resume. Suspending hands the terminal back to the
        subprocess so per-tool TUIs (slop-gh-key tui, etc.) can take over
        the screen the same way they do under the old fish/gum launcher."""
        if not action.argv:
            return
        with self.suspend():
            print()
            print("Equivalent CLI:")
            print(f"  {action.cli_text}")
            print()
            try:
                subprocess.call(action.argv)
            except KeyboardInterrupt:
                pass
            print()
            try:
                input("Press Enter to return to slop… ")
            except (EOFError, KeyboardInterrupt):
                pass


def main(argv: list[str] | None = None) -> int:
    SlopApp().run()
    return 0


if __name__ == "__main__":
    sys.exit(main())
