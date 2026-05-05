#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Helpers for scripts/slop-gh-key.fish.

Why this exists as a uv-managed script:
- Per project policy, all Python work must run under uv with a pinned
  interpreter so behavior is reproducible across machines.
- Subcommands map 1:1 to operations the fish wrapper used to inline as
  `python3 -c '...'`.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import sys
from pathlib import Path

_TTL_RX = re.compile(r"(\d+)([mhdw])")
_TTL_UNITS = {"m": "minutes", "h": "hours", "d": "days", "w": "weeks"}
_EXPIRY_RX = re.compile(r"(?:^|:)exp=([0-9T:\-]+Z)")


def _now_utc_iso() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def cmd_ttl_to_iso(args: argparse.Namespace) -> int:
    m = _TTL_RX.fullmatch(args.ttl)
    if not m:
        print(f"Invalid ttl: {args.ttl}", file=sys.stderr)
        return 1
    n = int(m.group(1))
    unit = _TTL_UNITS[m.group(2)]
    when = dt.datetime.now(dt.timezone.utc) + dt.timedelta(**{unit: n})
    print(when.replace(microsecond=0).isoformat().replace("+00:00", "Z"))
    return 0


def cmd_filter_by_title(args: argparse.Namespace) -> int:
    pattern = re.compile(args.pattern)
    data = json.load(sys.stdin)
    for k in data:
        if pattern.search(k.get("title", "")):
            print(k["id"])
    return 0


def cmd_filter_expired(_args: argparse.Namespace) -> int:
    now = dt.datetime.now(dt.timezone.utc)
    data = json.load(sys.stdin)
    for k in data:
        m = _EXPIRY_RX.search(k.get("title", ""))
        if not m:
            continue
        when = dt.datetime.fromisoformat(m.group(1).replace("Z", "+00:00"))
        if when <= now:
            print(k["id"])
    return 0


def cmd_ssh_config_uninstall(args: argparse.Namespace) -> int:
    cfg = Path(args.config_file)
    pattern = re.compile(args.pattern)
    lines = cfg.read_text().splitlines() if cfg.exists() else []
    out: list[str] = []
    skip = False
    removed: list[str] = []
    marker = ""
    for line in lines:
        if line.startswith("# BEGIN "):
            label = line[len("# BEGIN "):].strip()
            if pattern.search(label):
                skip = True
                marker = label
                removed.append(label)
                continue
        if skip:
            if marker and line.strip() == f"# END {marker}":
                skip = False
                marker = ""
            continue
        out.append(line)
    cfg.write_text("\n".join(out).rstrip() + "\n" if out else "")
    print(len(removed))
    for label in removed:
        print(label)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_ttl = sub.add_parser("ttl-to-iso", help="Convert TTL like 24h to UTC ISO timestamp.")
    p_ttl.add_argument("ttl")
    p_ttl.set_defaults(func=cmd_ttl_to_iso)

    p_ft = sub.add_parser("filter-by-title", help="Read gh keys JSON on stdin, print ids whose titles match.")
    p_ft.add_argument("pattern")
    p_ft.set_defaults(func=cmd_filter_by_title)

    p_fe = sub.add_parser("filter-expired", help="Read gh keys JSON on stdin, print ids whose embedded expiry has passed.")
    p_fe.set_defaults(func=cmd_filter_expired)

    p_su = sub.add_parser("ssh-config-uninstall", help="Remove our marker blocks from an ssh_config file.")
    p_su.add_argument("config_file")
    p_su.add_argument("pattern")
    p_su.set_defaults(func=cmd_ssh_config_uninstall)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
