#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Helpers for scripts/slop-forgejo-key.fish.

Why this exists as a uv-managed script:
- Per project policy, all Python work runs under uv with a pinned interpreter.
- Subcommands map 1:1 to operations the fish wrapper used to inline as
  `python3 -c '...'`.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import sys
import urllib.parse
from pathlib import Path

_TTL_RX = re.compile(r"(\d+)([mhdw])")
_TTL_UNITS = {"m": "minutes", "h": "hours", "d": "days", "w": "weeks"}
_EXPIRY_RX = re.compile(r"(?:^|:)exp=([0-9T:\-]+Z)")


def _read_doc(path: Path) -> dict:
    if not path.exists():
        return {"instances": {}}
    return json.loads(path.read_text())


def _write_doc(path: Path, doc: dict) -> None:
    path.write_text(json.dumps(doc, indent=2) + "\n")


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


def cmd_host_from_url(args: argparse.Namespace) -> int:
    parsed = urllib.parse.urlparse(args.url)
    print(parsed.hostname or "")
    return 0


def cmd_instance_set(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    doc.setdefault("instances", {})[args.name] = {"url": args.url, "token_env": args.token_env}
    _write_doc(path, doc)
    return 0


def cmd_instance_list(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    inst = doc.get("instances", {})
    if not inst:
        print("No Forgejo instance profiles configured.")
        return 0
    print("name\turl\ttoken_env")
    for name, cfg in sorted(inst.items()):
        print(f"{name}\t{cfg.get('url', '')}\t{cfg.get('token_env', '')}")
    return 0


def cmd_instance_remove(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    inst = doc.get("instances", {})
    if args.name in inst:
        del inst[args.name]
        _write_doc(path, doc)
        print(f"Removed Forgejo instance profile: {args.name}")
    else:
        print(f"Instance profile not found: {args.name}")
    return 0


def cmd_instance_get(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    cfg = doc.get("instances", {}).get(args.name)
    if not cfg:
        return 1
    print((cfg.get("url") or "") + "\t" + (cfg.get("token_env") or ""))
    return 0


def cmd_instance_by_host(args: argparse.Namespace) -> int:
    """Find the first instance profile whose URL host matches.

    Used by the fish wrapper's `here` shortcut: given the host parsed from
    the cwd's git origin, return the instance name + url + token_env so the
    user does not have to type --instance.
    """
    from urllib.parse import urlparse

    path = Path(args.config_file)
    doc = _read_doc(path)
    target = args.host.lower()
    for name, cfg in (doc.get("instances", {}) or {}).items():
        url = cfg.get("url") or ""
        host = (urlparse(url).hostname or "").lower()
        if host == target:
            print(name + "\t" + url + "\t" + (cfg.get("token_env") or ""))
            return 0
    return 1


def cmd_make_payload(args: argparse.Namespace) -> int:
    print(json.dumps({"title": args.title, "key": args.key, "read_only": args.read_only == "true"}))
    return 0


def cmd_parse_key_id(_args: argparse.Namespace) -> int:
    data = json.load(sys.stdin)
    print(data.get("id", ""))
    return 0


def cmd_list_keys(_args: argparse.Namespace) -> int:
    data = json.load(sys.stdin)
    for k in data:
        access = "ro" if k.get("read_only", True) else "rw"
        print(f"{k.get('id', '')}\t{access}\t{k.get('created_at', '')}\t{k.get('title', '')}")
    return 0


def cmd_filter_by_title(args: argparse.Namespace) -> int:
    pattern = re.compile(args.pattern)
    data = json.load(sys.stdin)
    for k in data:
        if pattern.search(k.get("title", "")):
            print(k.get("id", ""))
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
            print(k.get("id", ""))
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

    p_ttl = sub.add_parser("ttl-to-iso")
    p_ttl.add_argument("ttl")
    p_ttl.set_defaults(func=cmd_ttl_to_iso)

    p_host = sub.add_parser("host-from-url")
    p_host.add_argument("url")
    p_host.set_defaults(func=cmd_host_from_url)

    p_iset = sub.add_parser("instance-set")
    p_iset.add_argument("config_file")
    p_iset.add_argument("name")
    p_iset.add_argument("url")
    p_iset.add_argument("token_env")
    p_iset.set_defaults(func=cmd_instance_set)

    p_ilist = sub.add_parser("instance-list")
    p_ilist.add_argument("config_file")
    p_ilist.set_defaults(func=cmd_instance_list)

    p_irm = sub.add_parser("instance-remove")
    p_irm.add_argument("config_file")
    p_irm.add_argument("name")
    p_irm.set_defaults(func=cmd_instance_remove)

    p_iget = sub.add_parser("instance-get")
    p_iget.add_argument("config_file")
    p_iget.add_argument("name")
    p_iget.set_defaults(func=cmd_instance_get)

    p_ibh = sub.add_parser("instance-by-host")
    p_ibh.add_argument("config_file")
    p_ibh.add_argument("host")
    p_ibh.set_defaults(func=cmd_instance_by_host)

    p_mp = sub.add_parser("make-payload")
    p_mp.add_argument("title")
    p_mp.add_argument("key")
    p_mp.add_argument("read_only", help="literal 'true' or 'false'")
    p_mp.set_defaults(func=cmd_make_payload)

    p_pk = sub.add_parser("parse-key-id")
    p_pk.set_defaults(func=cmd_parse_key_id)

    p_lk = sub.add_parser("list-keys")
    p_lk.set_defaults(func=cmd_list_keys)

    p_fbt = sub.add_parser("filter-by-title")
    p_fbt.add_argument("pattern")
    p_fbt.set_defaults(func=cmd_filter_by_title)

    p_fe = sub.add_parser("filter-expired")
    p_fe.set_defaults(func=cmd_filter_expired)

    p_su = sub.add_parser("ssh-config-uninstall")
    p_su.add_argument("config_file")
    p_su.add_argument("pattern")
    p_su.set_defaults(func=cmd_ssh_config_uninstall)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
