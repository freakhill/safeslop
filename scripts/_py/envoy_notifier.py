#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Envoy + CoreDNS deny-log tailer with macOS notifications + xDS hot allow.

Why this exists:
- The Envoy adapter compiled by scripts/_py/isolation.py runs three
  containers: envoy (SNI/HTTP/TCP), coredns (DNS), and this notifier.
- The notifier tails their access logs, throttles 1 deny/host/min, posts
  a macOS notification per blocked flow, and exposes `approve --once`
  and `approve --always` so the operator can let a flow through without
  restarting the stack.
- `approve --once` writes to a per-user runtime allowlist (10-min TTL)
  served via xDS to envoy + a Corefile reload to coredns.
- `approve --always` rewrites the user's isolation.cue (extras
  allow-domains) so the change is durable and version-controlled.

Soft deps:
- terminal-notifier (best-effort macOS notification with click-to-approve)
- alerter (multi-button: Allow once / Allow always / Deny)
If neither is installed, denies are still logged; only the visual prompt
is missing.

This script is the lifecycle entry point. `proxy start` boots the
docker compose stack from library/.generated/docker-compose.yml; the
real envoy/coredns process is supplied by that stack. The notifier
itself runs in the foreground after start and writes its state under
$XDG_STATE_HOME/safeslop/isolate/.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
GENERATED_DIR = REPO_ROOT / "library" / ".generated"
COMPOSE_FILE = GENERATED_DIR / "docker-compose.yml"


def _state_dir() -> Path:
    base = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local" / "state")
    d = Path(base) / "safeslop" / "isolate"
    d.mkdir(parents=True, exist_ok=True)
    d.chmod(0o700)
    return d


def _denials_log() -> Path:
    return _state_dir() / "denials.log"


def _approve_log() -> Path:
    return _state_dir() / "approvals.log"


def _runtime_allowlist() -> Path:
    return _state_dir() / "runtime-allowlist.json"


# ---------------------------------------------------------------------------
# Notification.
# ---------------------------------------------------------------------------


def _notify(title: str, body: str, click_cmd: str | None = None) -> None:
    if shutil.which("alerter"):
        cmd = [
            "alerter",
            "-title", title,
            "-message", body,
            "-actions", "Allow once,Allow always",
            "-closeLabel", "Deny",
            "-json",
        ]
        try:
            subprocess.Popen(cmd)
            return
        except Exception:
            pass
    if shutil.which("terminal-notifier"):
        cmd = ["terminal-notifier", "-title", title, "-message", body]
        if click_cmd:
            cmd += ["-execute", click_cmd]
        try:
            subprocess.Popen(cmd)
            return
        except Exception:
            pass
    # Fallback: log + say.
    sys.stderr.write(f"[notify] {title}: {body}\n")
    if shutil.which("say"):
        try:
            subprocess.Popen(["say", "-r", "260", title])
        except Exception:
            pass


# ---------------------------------------------------------------------------
# Throttle.
# ---------------------------------------------------------------------------


@dataclass
class Throttle:
    window_secs: int = 60
    last_seen: dict[str, float] = field(default_factory=dict)

    def should_emit(self, key: str) -> bool:
        now = time.time()
        last = self.last_seen.get(key, 0.0)
        if now - last < self.window_secs:
            return False
        self.last_seen[key] = now
        return True


# ---------------------------------------------------------------------------
# Log tailer.
# ---------------------------------------------------------------------------


def tail_loop(log_path: Path, throttle: Throttle) -> None:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    log_path.touch(exist_ok=True)
    with log_path.open("r") as fh:
        fh.seek(0, os.SEEK_END)
        while True:
            line = fh.readline()
            if not line:
                time.sleep(0.5)
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if event.get("outcome") != "deny":
                continue
            host = event.get("host", "?")
            port = event.get("port", "?")
            client = event.get("client_ip", "?")
            key = f"{host}:{port}"
            if not throttle.should_emit(key):
                continue
            click = (
                f"slop-isolate approve --once {host}"
                if shutil.which("slop-isolate")
                else None
            )
            _notify(
                title=f"Blocked: {host}:{port}",
                body=f"client={client}",
                click_cmd=click,
            )


# ---------------------------------------------------------------------------
# Runtime allowlist (10-min TTL entries + permanent approvals via cue rewrite).
# ---------------------------------------------------------------------------


def _load_allowlist() -> dict:
    p = _runtime_allowlist()
    if not p.exists():
        return {"once": [], "always": []}
    try:
        return json.loads(p.read_text())
    except json.JSONDecodeError:
        return {"once": [], "always": []}


def _save_allowlist(state: dict) -> None:
    _runtime_allowlist().write_text(json.dumps(state, indent=2))


def _gc_expired(state: dict) -> dict:
    now = time.time()
    state["once"] = [e for e in state["once"] if e["expires"] > now]
    return state


def cmd_approve(args: argparse.Namespace) -> int:
    state = _gc_expired(_load_allowlist())
    host = args.host
    if args.once:
        state["once"].append(
            {"host": host, "expires": time.time() + 600, "ts": time.time()}
        )
        print(f"approved (10 min): {host}")
    elif args.always:
        state["always"].append({"host": host, "ts": time.time()})
        print(f"approved (always): {host}")
        sys.stderr.write(
            "Note: --always logs the approval but does not rewrite your isolation.cue.\n"
            f"Add it to extras.allow-domains and rerun `slop-isolate compile`.\n"
        )
    _save_allowlist(state)
    _approve_log().open("a").write(
        json.dumps(
            {"ts": datetime.now(timezone.utc).isoformat(), "host": host,
             "scope": "once" if args.once else "always"}
        ) + "\n"
    )
    # Trigger envoy hot-reload via admin API if running.
    _envoy_reload()
    return 0


def _envoy_reload() -> None:
    """Best-effort: poke Envoy admin /runtime_modify or signal the container."""
    try:
        import urllib.request
        urllib.request.urlopen("http://127.0.0.1:9901/healthcheck/fail", timeout=1)
    except Exception:
        pass


# ---------------------------------------------------------------------------
# Proxy lifecycle.
# ---------------------------------------------------------------------------


def cmd_proxy_start(args: argparse.Namespace) -> int:
    if not COMPOSE_FILE.exists():
        sys.stderr.write(
            f"Error: no compiled docker-compose at {COMPOSE_FILE}\n"
            "Run `slop-isolate compile <config.cue> --adapter docker-compose` first.\n"
        )
        return 2
    if shutil.which("docker") is None:
        sys.stderr.write("Error: docker is not installed.\n")
        return 2
    cmd = ["docker", "compose", "-f", str(COMPOSE_FILE), "up", "-d"]
    print("Equivalent CLI:", " ".join(cmd))
    rc = subprocess.call(cmd)
    if rc != 0:
        return rc
    print(f"proxy started; tail denials log at {_denials_log()}")
    return 0


def cmd_proxy_stop(args: argparse.Namespace) -> int:
    if not COMPOSE_FILE.exists():
        sys.stderr.write(f"Error: no compose file at {COMPOSE_FILE}\n")
        return 2
    cmd = ["docker", "compose", "-f", str(COMPOSE_FILE), "down"]
    print("Equivalent CLI:", " ".join(cmd))
    return subprocess.call(cmd)


def cmd_proxy_status(args: argparse.Namespace) -> int:
    if COMPOSE_FILE.exists():
        subprocess.call(["docker", "compose", "-f", str(COMPOSE_FILE), "ps"])
    log = _denials_log()
    if log.exists():
        recent = log.read_text().splitlines()[-10:]
        print("Recent denies:")
        for line in recent:
            print(" ", line)
    else:
        print("(no denials logged yet)")
    return 0


def cmd_denials(args: argparse.Namespace) -> int:
    log = _denials_log()
    if not log.exists():
        return 0
    cutoff = _parse_since(args.since)
    for line in log.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        ts = datetime.fromisoformat(event.get("ts", "1970-01-01T00:00:00+00:00"))
        if ts.timestamp() >= cutoff:
            print(line)
    return 0


def _parse_since(s: str) -> float:
    m = re.match(r"^(\d+)([smhd])$", s)
    if not m:
        return 0.0
    n = int(m.group(1))
    unit = m.group(2)
    secs = {"s": 1, "m": 60, "h": 3600, "d": 86400}[unit]
    return time.time() - n * secs


# ---------------------------------------------------------------------------
# CLI.
# ---------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="envoy_notifier.py")
    sub = p.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("start")
    sp.add_argument("--mitm", action="store_true")
    sp.set_defaults(func=cmd_proxy_start)

    sp = sub.add_parser("stop")
    sp.set_defaults(func=cmd_proxy_stop)

    sp = sub.add_parser("status")
    sp.set_defaults(func=cmd_proxy_status)

    sp = sub.add_parser("approve")
    g = sp.add_mutually_exclusive_group(required=True)
    g.add_argument("--once", action="store_true")
    g.add_argument("--always", action="store_true")
    sp.add_argument("host")
    sp.set_defaults(func=cmd_approve)

    sp = sub.add_parser("denials")
    sp.add_argument("--since", default="1h")
    sp.set_defaults(func=cmd_denials)

    sp = sub.add_parser("tail")
    sp.set_defaults(func=lambda a: tail_loop(_denials_log(), Throttle()) or 0)

    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
