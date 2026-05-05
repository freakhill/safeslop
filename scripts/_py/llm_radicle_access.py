#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Helpers for scripts/slop-radicle.fish.

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
import uuid
from pathlib import Path

_TTL_RX = re.compile(r"(\d+)([mhdw])")
_TTL_UNITS = {"m": "minutes", "h": "hours", "d": "days", "w": "weeks"}


def _now_iso() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _read_doc(path: Path) -> dict:
    if not path.exists():
        return {"identities": [], "bindings": []}
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


def cmd_uuid8(_args: argparse.Namespace) -> int:
    print(uuid.uuid4().hex[:8])
    return 0


def cmd_append_identity(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    doc.setdefault("identities", []).append(
        {
            "id": args.ident_id,
            "name": args.name,
            "key_path": args.key_path,
            "pub_path": args.pub_path,
            "created_at": _now_iso(),
            "expires_at": args.expiry,
            "status": "active",
        }
    )
    _write_doc(path, doc)
    return 0


def cmd_list_identities(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    print("id\tstatus\texpires_at\tname\tkey_path")
    for i in doc.get("identities", []):
        if not args.show_all and i.get("status") != "active":
            continue
        print(
            "{}\t{}\t{}\t{}\t{}".format(
                i.get("id", ""),
                i.get("status", ""),
                i.get("expires_at", ""),
                i.get("name", ""),
                i.get("key_path", ""),
            )
        )
    return 0


def cmd_retire_identity(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    found = False
    now = _now_iso()
    for i in doc.get("identities", []):
        if i.get("id") == args.ident_id:
            i["status"] = "retired"
            i["retired_at"] = now
            found = True
    if not found:
        return 1
    _write_doc(path, doc)
    return 0


def cmd_retire_expired(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    now = dt.datetime.now(dt.timezone.utc)
    now_iso = now.replace(microsecond=0).isoformat().replace("+00:00", "Z")
    changed: list[str] = []
    for i in doc.get("identities", []):
        if i.get("status") != "active":
            continue
        exp = i.get("expires_at")
        if not exp:
            continue
        when = dt.datetime.fromisoformat(exp.replace("Z", "+00:00"))
        if when <= now:
            i["status"] = "retired"
            i["retired_at"] = now_iso
            changed.append(i.get("id", ""))
    _write_doc(path, doc)
    for label in changed:
        print(label)
    return 0


def cmd_bind_repo(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    idents = {i.get("id"): i for i in doc.get("identities", [])}
    ident = idents.get(args.ident_id)
    if not ident or ident.get("status") != "active":
        return 2
    now = _now_iso()
    bindings = doc.setdefault("bindings", [])
    for b in bindings:
        if b.get("rid") == args.rid and b.get("identity_id") == args.ident_id:
            b["access"] = args.access
            b["note"] = args.note
            b["status"] = "active"
            b["updated_at"] = now
            _write_doc(path, doc)
            print("updated")
            return 0
    bindings.append(
        {
            "rid": args.rid,
            "identity_id": args.ident_id,
            "access": args.access,
            "note": args.note,
            "status": "active",
            "created_at": now,
        }
    )
    _write_doc(path, doc)
    print("created")
    return 0


def cmd_list_bindings(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    print("rid\tidentity_id\taccess\tstatus\tnote")
    for b in doc.get("bindings", []):
        if args.rid and b.get("rid") != args.rid:
            continue
        if not args.show_all and b.get("status") != "active":
            continue
        print(
            "{}\t{}\t{}\t{}\t{}".format(
                b.get("rid", ""),
                b.get("identity_id", ""),
                b.get("access", ""),
                b.get("status", ""),
                b.get("note", ""),
            )
        )
    return 0


def cmd_unbind_repo(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    now = _now_iso()
    changed: list[str] = []
    for b in doc.get("bindings", []):
        if b.get("rid") != args.rid:
            continue
        if args.ident_id and b.get("identity_id") != args.ident_id:
            continue
        if b.get("status") != "active":
            continue
        b["status"] = "retired"
        b["retired_at"] = now
        changed.append(f"{b.get('rid', '')}:{b.get('identity_id', '')}")
    _write_doc(path, doc)
    for label in changed:
        print(label)
    return 0


def cmd_get_active_key(args: argparse.Namespace) -> int:
    path = Path(args.config_file)
    doc = _read_doc(path)
    for i in doc.get("identities", []):
        if i.get("id") == args.ident_id and i.get("status") == "active":
            print(i.get("key_path", ""))
            return 0
    return 1


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_ttl = sub.add_parser("ttl-to-iso")
    p_ttl.add_argument("ttl")
    p_ttl.set_defaults(func=cmd_ttl_to_iso)

    p_uuid = sub.add_parser("uuid8")
    p_uuid.set_defaults(func=cmd_uuid8)

    p_app = sub.add_parser("append-identity")
    p_app.add_argument("config_file")
    p_app.add_argument("ident_id")
    p_app.add_argument("name")
    p_app.add_argument("key_path")
    p_app.add_argument("pub_path")
    p_app.add_argument("expiry")
    p_app.set_defaults(func=cmd_append_identity)

    p_li = sub.add_parser("list-identities")
    p_li.add_argument("config_file")
    p_li.add_argument("--show-all", action="store_true")
    p_li.set_defaults(func=cmd_list_identities)

    p_ret = sub.add_parser("retire-identity")
    p_ret.add_argument("config_file")
    p_ret.add_argument("ident_id")
    p_ret.set_defaults(func=cmd_retire_identity)

    p_re = sub.add_parser("retire-expired")
    p_re.add_argument("config_file")
    p_re.set_defaults(func=cmd_retire_expired)

    p_bind = sub.add_parser("bind-repo")
    p_bind.add_argument("config_file")
    p_bind.add_argument("rid")
    p_bind.add_argument("ident_id")
    p_bind.add_argument("access")
    p_bind.add_argument("note")
    p_bind.set_defaults(func=cmd_bind_repo)

    p_lb = sub.add_parser("list-bindings")
    p_lb.add_argument("config_file")
    p_lb.add_argument("rid", nargs="?", default="")
    p_lb.add_argument("--show-all", action="store_true")
    p_lb.set_defaults(func=cmd_list_bindings)

    p_un = sub.add_parser("unbind-repo")
    p_un.add_argument("config_file")
    p_un.add_argument("rid")
    p_un.add_argument("ident_id", nargs="?", default="")
    p_un.set_defaults(func=cmd_unbind_repo)

    p_gak = sub.add_parser("get-active-key")
    p_gak.add_argument("config_file")
    p_gak.add_argument("ident_id")
    p_gak.set_defaults(func=cmd_get_active_key)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
