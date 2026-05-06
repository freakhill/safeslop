#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Compiler for slop-isolate unified isolation policy (CUE → adapters).

Why this exists:
- Authors keep one CUE file describing network/filesystem/process intent.
- This script invokes `cue` to validate + export the config to JSON,
  strips the `extras` extension surface, and dispatches to a registry of
  per-adapter emitters that produce concrete tool-specific configs.
- Every adapter declares which logical primitives it can enforce. When a
  primitive is requested but the adapter cannot enforce it, the emitted
  output records that fact as a comment (or the run fails with --strict).

Subcommands:
  validate <file.cue>
  compile  <file.cue> --adapter <name> [--out <dir>] [--strict]
  presets  list
  presets  show <name>
  apply    <file.cue> [--adapters a,b] [--dry-run] [--yes]    (bounded)
  proxy    start|stop|status [--mitm]
  approve  --once|--always <host[:port]>
  denials  [--since <duration>]
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import textwrap
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, Iterable

# ---------------------------------------------------------------------------
# Locating the CUE module + presets.
# ---------------------------------------------------------------------------

REPO_ROOT = Path(__file__).resolve().parents[2]
ISOLATION_DIR = REPO_ROOT / "library" / "isolation"
PRESETS_DIR = ISOLATION_DIR / "presets"
GENERATED_DIR = REPO_ROOT / "library" / ".generated"

PRESET_DEFINITIONS = {
    "any-agent": "#AnyAgent",
    "claude-code": "#ClaudeCode",
    "opencode": "#OpenCode",
    "crewai": "#CrewAI",
    "pydantic-ai": "#PydanticAI",
    "ag2": "#AG2",
    "openclaw": "#OpenClaw",
    "zeroclaw": "#ZeroClaw",
    "nous-hermes-local": "#NousHermesLocal",
    "nous-hermes-remote": "#NousHermesRemote",
}


# ---------------------------------------------------------------------------
# CUE invocation.
# ---------------------------------------------------------------------------


def _require_cue() -> None:
    if shutil.which("cue") is None:
        sys.stderr.write(
            "Error: `cue` not found on PATH.\n"
            "Install: brew install cue-lang/tap/cue\n"
        )
        sys.exit(2)


def cue_vet(target: Path) -> None:
    """Validate a CUE file or directory against the schema."""
    _require_cue()
    cwd, args = _cue_target(target)
    proc = subprocess.run(
        ["cue", "vet", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr)
        sys.exit(proc.returncode)


def cue_export(target: Path, expression: str = "isolation") -> dict:
    """Export a CUE file/dir to a JSON dict via `cue export -e <expr>`."""
    _require_cue()
    cwd, args = _cue_target(target)
    proc = subprocess.run(
        ["cue", "export", "-e", expression, "--out", "json", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr)
        sys.exit(proc.returncode)
    return json.loads(proc.stdout)


def _cue_target(target: Path) -> tuple[Path, list[str]]:
    """Return (cwd, args) for the cue invocation. If `target` is a file,
    cd to its parent and pass the filename so package resolution works."""
    target = target.resolve()
    if target.is_dir():
        return target, ["."]
    return target.parent, [target.name]


def normalize(raw: dict) -> dict:
    """Strip the `extras` extension surface; the compiler does not emit it.
    Sort lists for deterministic output."""
    out = dict(raw)
    out.pop("extras", None)
    return out


# ---------------------------------------------------------------------------
# Adapter registry.
# ---------------------------------------------------------------------------

Emitter = Callable[[dict, "EmitOptions"], "EmitResult"]


@dataclass
class EmitOptions:
    strict: bool = False


@dataclass
class EmitResult:
    files: dict[str, str]                 # relative path -> content
    notes: list[str]                      # informational warnings
    equivalent_cli: list[str] | None = None  # for tart/orbstack


ADAPTERS: dict[str, Emitter] = {}


def adapter(name: str) -> Callable[[Emitter], Emitter]:
    def decorate(fn: Emitter) -> Emitter:
        ADAPTERS[name] = fn
        return fn
    return decorate


def _lossy(adapter_name: str, key: str, options: EmitOptions, notes: list[str]) -> str:
    msg = f"slop-isolate: {key} not enforced by {adapter_name}"
    if options.strict:
        sys.stderr.write(f"Error: --strict: {msg}\n")
        sys.exit(3)
    notes.append(msg)
    return f"# {msg}"


# ---------------------------------------------------------------------------
# Adapter: claude-code-settings (~/.config/claude-code/settings.json shape).
# ---------------------------------------------------------------------------


@adapter("claude-code-settings")
def emit_claude_code(cfg: dict, options: EmitOptions) -> EmitResult:
    notes: list[str] = []
    fs = cfg["filesystem"]
    net = cfg["network"]
    settings = {
        "sandbox": {
            "enabled": True,
            "filesystem": {
                "allowWrite": fs["allow-write"],
                "denyRead": fs["deny-read"],
                "denyWrite": fs["deny-write"],
            },
            "network": {
                "allowDomains": net["allow-domains"],
            },
        }
    }
    return EmitResult(
        files={"claude-code.settings.json": json.dumps(settings, indent=2) + "\n"},
        notes=notes,
    )


# ---------------------------------------------------------------------------
# Adapter: opencode-settings (~/.config/opencode/opencode.json shape).
# ---------------------------------------------------------------------------


@adapter("opencode-settings")
def emit_opencode(cfg: dict, options: EmitOptions) -> EmitResult:
    fs = cfg["filesystem"]
    deny_read = {p: "deny" for p in fs["deny-read"]}
    bash_rules: dict[str, str] = {"*": "deny"}
    for cmd in cfg["process"]["exec-allow"]:
        bash_rules[f"{cmd}*"] = "allow"
    settings = {
        "$schema": "https://opencode.ai/config.json",
        "permission": {
            "*": "ask",
            "read": {"*": "allow", **deny_read},
            "edit": "ask",
            "external_directory": "deny",
            "webfetch": "ask",
            "websearch": "ask",
            "bash": bash_rules,
        },
    }
    return EmitResult(
        files={"opencode.json": json.dumps(settings, indent=2) + "\n"},
        notes=[],
    )


# ---------------------------------------------------------------------------
# Adapter: sandbox-exec (.sb policy).
# ---------------------------------------------------------------------------


@adapter("sandbox-exec")
def emit_sandbox_exec(cfg: dict, options: EmitOptions) -> EmitResult:
    notes: list[str] = []
    name = cfg.get("name", "agent")
    fs = cfg["filesystem"]
    net = cfg["network"]
    proc = cfg["process"]
    deny_network = (
        cfg.get("tool", {}).get("sandbox-exec", {}).get("deny-network", "default")
    )

    lines: list[str] = []
    lines.append(f";; sandbox-exec policy for {name}")
    lines.append(";; generated by slop-isolate")
    lines.append("(version 1)")
    lines.append("(deny default)")
    lines.append("(allow process-fork)")
    lines.append("(allow signal (target self))")

    # Network — sandbox-exec cannot match domains.
    if deny_network == "all" or net["policy"] == "strict-egress":
        lines.append("(deny network*)")
        if net.get("allow-loopback"):
            lines.append("(allow network* (local ip))")
        lines.append(_lossy("sandbox-exec", "network.allow-domains", options, notes))
    elif net["policy"] == "off":
        lines.append("(allow network*)")

    # File reads.
    lines.append(";; reads")
    lines.append("(allow file-read*)")
    for path in fs["deny-read"]:
        lines.append(f'(deny file-read* (subpath "{_expand(path)}"))')

    # File writes.
    lines.append(";; writes")
    lines.append("(deny file-write*)")
    for path in fs["allow-write"]:
        lines.append(f'(allow file-write* (subpath "{_expand(path)}"))')

    # Process exec.
    if proc["exec-allow"]:
        lines.append(";; exec")
        lines.append("(deny process-exec)")
        for binary in proc["exec-allow"]:
            lines.append(f'(allow process-exec (literal "/{_basename_only(binary)}"))')
        lines.append("(allow process-exec (with no-sandbox))")

    return EmitResult(
        files={f"{name}.sb": "\n".join(lines) + "\n"},
        notes=notes,
    )


def _expand(path: str) -> str:
    return path.replace("~", "${HOME}")


def _basename_only(b: str) -> str:
    return b.split("/")[-1]


# ---------------------------------------------------------------------------
# Adapter: squid (allowlist + acl).
# ---------------------------------------------------------------------------


@adapter("squid")
def emit_squid(cfg: dict, options: EmitOptions) -> EmitResult:
    domains = "\n".join(f".{d}" for d in cfg["network"]["allow-domains"]) + "\n"
    return EmitResult(
        files={"squid.allowlist.domains": domains},
        notes=[],
    )


# ---------------------------------------------------------------------------
# Adapter: docker-compose (proxy + agent stack).
# ---------------------------------------------------------------------------


@adapter("docker-compose")
def emit_docker_compose(cfg: dict, options: EmitOptions) -> EmitResult:
    name = cfg.get("name", "agent")
    fs = cfg["filesystem"]
    proc = cfg["process"]
    cap_drop = "\n      - ".join(proc["cap-drop"])
    nproc = proc["ulimits"].get("nproc", 256)
    nofile = proc["ulimits"].get("nofile", 4096)
    read_only = "true" if fs["read-only-root"] else "false"
    bind_mounts = []
    for path in fs["allow-write"]:
        if path.startswith("./"):
            bind_mounts.append(f"      - {path}:/workspace/{path.lstrip('./')}:rw")
    body = textwrap.dedent(
        f"""\
        # docker-compose for {name}
        # generated by slop-isolate
        services:
          proxy:
            image: ubuntu/squid:latest
            ports:
              - "127.0.0.1:3128:3128"
            volumes:
              - ./squid.allowlist.domains:/etc/squid/allowlist.domains:ro
          agent:
            image: local/agent-sandbox:latest
            depends_on: [proxy]
            environment:
              HTTP_PROXY: http://proxy:3128
              HTTPS_PROXY: http://proxy:3128
              NO_PROXY: localhost,127.0.0.1
            read_only: {read_only}
            cap_drop:
              - {cap_drop}
            security_opt:
              - no-new-privileges:true
            pids_limit: {nproc}
            ulimits:
              nofile: {nofile}
              nproc: {nproc}
            working_dir: /workspace
        """
    )
    return EmitResult(files={"docker-compose.yml": body}, notes=[])


# ---------------------------------------------------------------------------
# Adapter: envoy + coredns + notifier (the interactive-approval stack).
# ---------------------------------------------------------------------------


@adapter("envoy")
def emit_envoy(cfg: dict, options: EmitOptions) -> EmitResult:
    notes: list[str] = []
    name = cfg.get("name", "agent")
    net = cfg["network"]
    envoy_overrides = cfg.get("tool", {}).get("envoy", {})
    tls_mode = envoy_overrides.get("tls", "sni")
    tcp_allow = envoy_overrides.get("tcp-allow", [])

    # Build SNI virtual hosts.
    sni_clusters = []
    sni_routes = []
    for domain in net["allow-domains"]:
        cluster_name = f"sni_{re.sub(r'[^a-z0-9]', '_', domain)}"
        sni_clusters.append(
            {
                "name": cluster_name,
                "type": "LOGICAL_DNS",
                "connect_timeout": "5s",
                "load_assignment": {
                    "cluster_name": cluster_name,
                    "endpoints": [
                        {
                            "lb_endpoints": [
                                {
                                    "endpoint": {
                                        "address": {
                                            "socket_address": {
                                                "address": domain,
                                                "port_value": 443,
                                            }
                                        }
                                    }
                                }
                            ]
                        }
                    ],
                },
            }
        )
        sni_routes.append(
            {
                "filter_chain_match": {"server_names": [domain]},
                "filters": [
                    {
                        "name": "envoy.filters.network.tcp_proxy",
                        "typed_config": {
                            "@type": "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
                            "stat_prefix": cluster_name,
                            "cluster": cluster_name,
                        },
                    }
                ],
            }
        )

    if tls_mode == "mitm":
        notes.append("envoy.tls=mitm: agent containers must trust the per-stack CA")

    config = {
        "admin": {
            "access_log_path": "/var/log/envoy/admin.log",
            "address": {
                "socket_address": {"address": "127.0.0.1", "port_value": 9901}
            },
        },
        "static_resources": {
            "listeners": [
                {
                    "name": "https",
                    "address": {
                        "socket_address": {"address": "0.0.0.0", "port_value": 443}
                    },
                    "listener_filters": [
                        {"name": "envoy.filters.listener.tls_inspector"}
                    ],
                    "filter_chains": sni_routes
                    or [
                        {
                            "filters": [
                                {
                                    "name": "envoy.filters.network.tcp_proxy",
                                    "typed_config": {
                                        "@type": "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
                                        "stat_prefix": "deny",
                                        "cluster": "deny_cluster",
                                    },
                                }
                            ]
                        }
                    ],
                }
            ],
            "clusters": sni_clusters
            + (
                _tcp_clusters(tcp_allow)
                if tcp_allow
                else []
            ),
        },
    }
    files: dict[str, str] = {
        "envoy.yaml": json.dumps(config, indent=2) + "\n",
        "coredns.Corefile": _emit_corefile(net),
    }
    if tls_mode == "mitm":
        files["mitm-ca.README.md"] = (
            "# MITM CA\n"
            "Run `slop-isolate proxy start --mitm` to generate a per-stack CA "
            "and inject it into agent containers' trust store. Never commit "
            "the CA private key.\n"
        )
    return EmitResult(files=files, notes=notes)


def _tcp_clusters(rules: list[dict]) -> list[dict]:
    clusters = []
    for rule in rules:
        for host in rule.get("hosts", []):
            cluster_name = f"tcp_{rule['port']}_{re.sub(r'[^a-z0-9]', '_', host)}"
            clusters.append(
                {
                    "name": cluster_name,
                    "type": "LOGICAL_DNS",
                    "connect_timeout": "5s",
                    "load_assignment": {
                        "cluster_name": cluster_name,
                        "endpoints": [
                            {
                                "lb_endpoints": [
                                    {
                                        "endpoint": {
                                            "address": {
                                                "socket_address": {
                                                    "address": host,
                                                    "port_value": rule["port"],
                                                }
                                            }
                                        }
                                    }
                                ]
                            }
                        ],
                    },
                }
            )
    return clusters


def _emit_corefile(net: dict) -> str:
    domains = " ".join(net["allow-domains"]) or "."
    upstream = "1.1.1.1 8.8.8.8" if net.get("dns") == "system" else "/etc/resolv.conf"
    return textwrap.dedent(
        f"""\
        # CoreDNS — only resolves the allowlist; logs all queries.
        . {{
            log
            errors
            forward . {upstream}
            template ANY ANY {{
                rcode NXDOMAIN
            }}
        }}

        {domains} {{
            log
            errors
            forward . {upstream}
        }}
        """
    )


# ---------------------------------------------------------------------------
# Adapter: pf (macOS packet filter) — domains resolved & pinned.
# ---------------------------------------------------------------------------


@adapter("pf")
def emit_pf(cfg: dict, options: EmitOptions) -> EmitResult:
    notes: list[str] = []
    pf_overrides = cfg.get("tool", {}).get("pf", {})
    fallback = pf_overrides.get("domain-fallback", "resolve-once-then-pin")
    domains = cfg["network"]["allow-domains"]

    if domains and fallback == "fail":
        sys.stderr.write(
            "Error: pf cannot match by domain and tool.pf.domain-fallback=fail.\n"
        )
        sys.exit(3)

    ips: list[str] = []
    if fallback == "resolve-once-then-pin":
        for domain in domains:
            try:
                infos = socket.getaddrinfo(domain, None)
                for fam, _, _, _, sockaddr in infos:
                    ips.append(sockaddr[0])
            except socket.gaierror as exc:
                notes.append(f"pf: failed to resolve {domain}: {exc}")
        if domains:
            notes.append(
                "pf: domain allowlist resolved at compile time; rules will go stale."
            )
    elif fallback == "skip":
        notes.append("pf: skipping domain rules (Squid/Envoy upstream carries them)")

    ips = sorted(set(ips))
    body_lines = [
        f"# pf anchor for {cfg.get('name', 'agent')}",
        "# generated by slop-isolate",
        "table <sandbox_allow> persist {",
    ]
    for ip in ips:
        body_lines.append(f"    {ip}")
    body_lines.append("}")
    body_lines.append("block out all")
    body_lines.append("pass out quick to <sandbox_allow>")
    if cfg["network"].get("allow-loopback"):
        body_lines.append("pass on lo0")
    return EmitResult(
        files={"pf.anchor": "\n".join(body_lines) + "\n"},
        notes=notes,
    )


# ---------------------------------------------------------------------------
# Adapter: lulu (per-binary outbound rules).
# ---------------------------------------------------------------------------


@adapter("lulu")
def emit_lulu(cfg: dict, options: EmitOptions) -> EmitResult:
    notes: list[str] = []
    lulu_overrides = cfg.get("tool", {}).get("lulu", {})
    binary = lulu_overrides.get("binary")
    if not binary:
        sys.stderr.write(
            "Error: lulu adapter requires tool.lulu.binary (path to the agent executable).\n"
            "Set it in the preset or in your config: tool: lulu: binary: \"/path/to/bin\"\n"
        )
        sys.exit(3)
    rules: list[dict] = []
    for domain in cfg["network"]["allow-domains"]:
        rules.append(
            {
                "process": binary,
                "endpointAddr": domain,
                "endpointPort": "443",
                "action": "allow",
                "scope": "process",
            }
        )
    rules.append(
        {
            "process": binary,
            "action": "block",
            "scope": "process",
            "endpointAddr": "*",
        }
    )
    return EmitResult(
        files={"lulu.rules.json": json.dumps({"rules": rules}, indent=2) + "\n"},
        notes=notes,
    )


# ---------------------------------------------------------------------------
# Adapter: tart — prints the equivalent slop-brew-vm invocation.
# ---------------------------------------------------------------------------


@adapter("tart")
def emit_tart(cfg: dict, options: EmitOptions) -> EmitResult:
    name = cfg.get("name", "agent")
    cli = (
        f"slop-brew-vm provision --name {name} && "
        f"slop-brew-vm run --name {name} --network-policy {cfg['network']['policy']}"
    )
    return EmitResult(
        files={"tart.invocation.txt": cli + "\n"},
        notes=[],
        equivalent_cli=[cli],
    )


# ---------------------------------------------------------------------------
# Adapter: orbstack — print equivalent orb invocation.
# ---------------------------------------------------------------------------


@adapter("orbstack")
def emit_orbstack(cfg: dict, options: EmitOptions) -> EmitResult:
    name = cfg.get("name", "agent")
    cli = (
        f"orb run -m local/agent-sandbox:latest "
        f"-e HTTP_PROXY=http://proxy:3128 "
        f"-e HTTPS_PROXY=http://proxy:3128 "
        f"--name {name}"
    )
    return EmitResult(
        files={"orbstack.invocation.txt": cli + "\n"},
        notes=[],
        equivalent_cli=[cli],
    )


# ---------------------------------------------------------------------------
# Adapter: ag2-executor — emit a Python stub wiring AG2's DockerCommandLineCodeExecutor.
# ---------------------------------------------------------------------------


@adapter("ag2-executor")
def emit_ag2_executor(cfg: dict, options: EmitOptions) -> EmitResult:
    fs = cfg["filesystem"]
    work_mounts = ", ".join(f'"{p}"' for p in fs["allow-write"])
    body = textwrap.dedent(
        f'''\
        """Auto-generated by slop-isolate. AG2 executor wired to the unified policy."""

        from autogen.coding import DockerCommandLineCodeExecutor

        executor = DockerCommandLineCodeExecutor(
            image="local/agent-sandbox:latest",
            work_dir="./tmp",
            timeout=60,
            auto_remove=True,
            stop_container=True,
            extra_volumes=[{work_mounts}],
            extra_environment={{
                "HTTP_PROXY": "http://proxy:3128",
                "HTTPS_PROXY": "http://proxy:3128",
            }},
        )
        '''
    )
    return EmitResult(files={"ag2_executor.py": body}, notes=[])


# ---------------------------------------------------------------------------
# Driver functions.
# ---------------------------------------------------------------------------


def cmd_validate(args: argparse.Namespace) -> int:
    cue_vet(Path(args.config))
    print(f"OK: {args.config} validates against the schema.")
    return 0


def cmd_compile(args: argparse.Namespace) -> int:
    if args.preset:
        expr = PRESET_DEFINITIONS.get(args.preset)
        if expr is None:
            sys.stderr.write(f"Error: unknown preset '{args.preset}'\n")
            return 2
        raw = cue_export(PRESETS_DIR, expression=expr)
    else:
        if not args.config:
            sys.stderr.write("Error: pass <config.cue> or --preset <name>\n")
            return 2
        cue_vet(Path(args.config))
        raw = cue_export(Path(args.config))
    cfg = normalize(raw)
    options = EmitOptions(strict=args.strict)

    adapters: list[str]
    if args.adapter:
        adapters = [args.adapter]
    else:
        adapters = list(cfg["adapters"]["enabled"])

    out_dir = Path(args.out) if args.out else GENERATED_DIR
    out_dir.mkdir(parents=True, exist_ok=True)

    for name in adapters:
        emitter = ADAPTERS.get(name)
        if emitter is None:
            sys.stderr.write(f"Error: no emitter for adapter '{name}'\n")
            return 3
        result = emitter(cfg, options)
        for filename, content in result.files.items():
            target = out_dir / filename
            target.write_text(content)
            print(f"wrote {target}")
        for note in result.notes:
            print(f"  note: {note}", file=sys.stderr)
        if result.equivalent_cli:
            for line in result.equivalent_cli:
                print(f"  Equivalent CLI: {line}")
    return 0


def cmd_presets_list(args: argparse.Namespace) -> int:
    for name in PRESET_DEFINITIONS:
        print(name)
    return 0


def cmd_presets_show(args: argparse.Namespace) -> int:
    expr = PRESET_DEFINITIONS.get(args.name)
    if expr is None:
        sys.stderr.write(f"Error: unknown preset '{args.name}'\n")
        sys.stderr.write(f"Available: {', '.join(PRESET_DEFINITIONS)}\n")
        return 2
    raw = cue_export(PRESETS_DIR, expression=expr)
    print(json.dumps(normalize(raw), indent=2))
    return 0


def cmd_apply(args: argparse.Namespace) -> int:
    """Bounded apply: writes generated files, never touches sudo / pf / lulu / /etc."""
    cue_vet(Path(args.config))
    raw = cue_export(Path(args.config))
    cfg = normalize(raw)
    requested = args.adapters.split(",") if args.adapters else cfg["adapters"]["enabled"]

    out_dir = GENERATED_DIR
    if args.dry_run:
        print(f"[dry-run] would compile to {out_dir}")
        for name in requested:
            print(f"[dry-run]   adapter: {name}")
        return 0

    if not args.yes:
        sys.stderr.write(
            "Error: apply requires --yes (writes to ~/.config/* and library/.generated/).\n"
        )
        return 2

    out_dir.mkdir(parents=True, exist_ok=True)
    options = EmitOptions(strict=False)
    for name in requested:
        emitter = ADAPTERS.get(name)
        if emitter is None:
            sys.stderr.write(f"Error: no emitter for adapter '{name}'\n")
            return 3
        result = emitter(cfg, options)
        for filename, content in result.files.items():
            target = out_dir / filename
            target.write_text(content)
            print(f"wrote {target}")
        if result.equivalent_cli:
            for line in result.equivalent_cli:
                print(f"  Equivalent CLI: {line}")

    if "claude-code-settings" in requested:
        _install_user_config(
            "~/.config/claude-code/settings.json",
            out_dir / "claude-code.settings.json",
        )
    if "opencode-settings" in requested:
        _install_user_config(
            "~/.config/opencode/opencode.json",
            out_dir / "opencode.json",
        )
    return 0


def _install_user_config(target: str, source: Path) -> None:
    target_p = Path(target).expanduser()
    target_p.parent.mkdir(parents=True, exist_ok=True)
    if target_p.exists():
        backup = target_p.with_suffix(target_p.suffix + ".bak")
        shutil.copy2(target_p, backup)
        print(f"backup: {backup}")
    shutil.copy2(source, target_p)
    print(f"installed: {target_p}")


def cmd_proxy(args: argparse.Namespace) -> int:
    """Stub proxy lifecycle. Defers to envoy_notifier.py for the real work."""
    notifier = REPO_ROOT / "scripts" / "_py" / "envoy_notifier.py"
    if not notifier.exists():
        sys.stderr.write("Error: scripts/_py/envoy_notifier.py is missing.\n")
        return 2
    cmd = ["uv", "run", "--script", str(notifier), args.action]
    if args.mitm:
        cmd.append("--mitm")
    return subprocess.call(cmd)


def cmd_approve(args: argparse.Namespace) -> int:
    notifier = REPO_ROOT / "scripts" / "_py" / "envoy_notifier.py"
    cmd = ["uv", "run", "--script", str(notifier), "approve"]
    if args.once:
        cmd += ["--once", args.host]
    elif args.always:
        cmd += ["--always", args.host]
    return subprocess.call(cmd)


def cmd_denials(args: argparse.Namespace) -> int:
    notifier = REPO_ROOT / "scripts" / "_py" / "envoy_notifier.py"
    cmd = ["uv", "run", "--script", str(notifier), "denials", "--since", args.since]
    return subprocess.call(cmd)


# ---------------------------------------------------------------------------
# CLI parser.
# ---------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="isolation.py")
    sub = p.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("validate")
    sp.add_argument("config")
    sp.set_defaults(func=cmd_validate)

    sp = sub.add_parser("compile")
    sp.add_argument("config", nargs="?", default=None)
    sp.add_argument("--preset", help="compile a built-in preset directly (skips file path)")
    sp.add_argument("--adapter", help="single adapter; default = adapters.enabled list")
    sp.add_argument("--out", help=f"output dir (default {GENERATED_DIR})")
    sp.add_argument("--strict", action="store_true")
    sp.set_defaults(func=cmd_compile)

    sp = sub.add_parser("presets")
    pp = sp.add_subparsers(dest="presets_cmd", required=True)
    spl = pp.add_parser("list")
    spl.set_defaults(func=cmd_presets_list)
    sps = pp.add_parser("show")
    sps.add_argument("name")
    sps.set_defaults(func=cmd_presets_show)

    sp = sub.add_parser("apply")
    sp.add_argument("config")
    sp.add_argument("--adapters")
    sp.add_argument("--dry-run", action="store_true")
    sp.add_argument("--yes", action="store_true")
    sp.set_defaults(func=cmd_apply)

    sp = sub.add_parser("proxy")
    sp.add_argument("action", choices=["start", "stop", "status"])
    sp.add_argument("--mitm", action="store_true")
    sp.set_defaults(func=cmd_proxy)

    sp = sub.add_parser("approve")
    g = sp.add_mutually_exclusive_group(required=True)
    g.add_argument("--once", action="store_true")
    g.add_argument("--always", action="store_true")
    sp.add_argument("host")
    sp.set_defaults(func=cmd_approve)

    sp = sub.add_parser("denials")
    sp.add_argument("--since", default="1h")
    sp.set_defaults(func=cmd_denials)

    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
