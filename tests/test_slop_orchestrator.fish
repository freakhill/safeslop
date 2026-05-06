#!/usr/bin/env fish

# Tests for scripts/_py/slop_orchestrator.py and the slop.fish dispatch
# that fronts it (Phase D MVP, host-only).
#
# We never spawn a real Claude / OpenCode REPL — those are interactive
# binaries that need a TTY, which CI doesn't have. Tests focus on the
# pure-pre-launch surface: argparse, slop.cue resolution, schema
# evaluation via cue, profile parsing, error paths, and the slop.fish
# dispatch (does `slop run` reach the orchestrator? does bare `slop`
# in a slop.cue-bearing repo? does the absence of slop.cue still hit
# the TUI gate as before?).

source (dirname (status filename))/helpers.fish

set -g ORCH_PY "$SCRIPTS_DIR/_py/slop_orchestrator.py"
set -g SLOP_FISH "$SCRIPTS_DIR/slop.fish"

# Convenience: run the orchestrator directly via uv. Reused by every
# test that exercises subcommands.
function __orch
    env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --script --quiet "$ORCH_PY" $argv 2>&1
end

# Convenience: `slop` (the fish wrapper) with cwd set to a tmp repo.
function __slop_in
    set -l cwd "$argv[1]"
    set -e argv[1]
    set -l body "
        cd '$cwd'
        set -x ATB_USER_PWD '$cwd'
        fish '$SLOP_FISH' $argv
    "
    command fish -N -c "$body" 2>&1
end

function __have_uv_and_cue
    command -sq uv; and command -sq cue
end

function test_help_lists_orchestrator_subcommands
    set -l out (__orch --help)
    set -l rc $status
    assert_status "orch --help status" $rc 0
    for sub in validate list run down
        assert_contains "orch --help mentions $sub" "$out" "$sub"
    end
end

function test_validate_without_slop_cue_fails_clearly
    if not __have_uv_and_cue
        __test_record_pass "orch validate without cue (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' validate
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "orch validate fails when slop.cue missing" $rc 1
    assert_contains "error names slop.cue" "$out" "slop.cue"
end

function test_validate_against_bundled_sample
    # The sample slop.cue under library/layer/policy/samples/slop/ is the
    # canonical reference. Drop a copy into a tmp repo and validate.
    if not __have_uv_and_cue
        __test_record_pass "orch validate sample (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' validate
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "orch validate sample passes" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "orch validate sample passes"
    assert_contains "validate reports profile count" "$out" "profiles: 2"
    assert_contains "validate reports default name" "$out" "default:  review"
end

function test_list_marks_default_profile
    if not __have_uv_and_cue
        __test_record_pass "orch list marks default (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' list
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "orch list status" $rc 0
    # The default profile gets a leading '*'; the others get a leading space.
    assert_contains "list marks default with asterisk" "$out" "* review"
    assert_contains "list shows non-default profile" "$out" "  explore"
end

function test_run_unknown_profile_fails_with_helpful_error
    if not __have_uv_and_cue
        __test_record_pass "orch run unknown (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run nonexistent
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "orch run unknown fails" $rc 1
    assert_contains "error names the bad profile" "$out" "nonexistent"
    assert_contains "error lists available profiles" "$out" "review"
end

function test_run_container_profile_dry_run
    # Phase E: container profiles run end-to-end. The test uses
    # --dry-run so it does not require docker on the test runner.
    # The orchestrator should print the equivalent CLI for both the
    # `up` (image build + proxy start) and the `run <agent>` step.
    if not __have_uv_and_cue
        __test_record_pass "orch run container dry-run (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run review --dry-run
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "orch run review --dry-run status" $rc 0
    assert_contains "dry-run announces container env" "$out" "env=container"
    assert_contains "dry-run prints agent-sandbox-tools up" "$out" "slop-agent-sandbox-tools up"
    assert_contains "dry-run prints run agent" "$out" "slop-agent-sandbox-tools run claude"
    assert_contains "dry-run notes credentials would be provisioned" "$out" "credentials"
    assert_contains "dry-run says provisioning is skipped" "$out" "skipped"
end

function test_run_host_profile_dry_run
    # Same shape, host environment. The "explore" profile in the
    # bundled sample is environment=host, agent=opencode.
    if not __have_uv_and_cue
        __test_record_pass "orch run host dry-run (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run explore --dry-run
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "orch run explore --dry-run status" $rc 0
    assert_contains "dry-run announces host env" "$out" "env=host"
    assert_contains "dry-run prints slop-agents opencode" "$out" "slop-agents opencode"
end

function test_run_vm_profile_dry_run
    # Phase G: vm profiles dispatch to slop-brew-vm. Use --dry-run so
    # the test does not require Tart on the runner; we just want to
    # confirm the orchestrator no longer bails out and prints the
    # right equivalent CLI for the init + run pair.
    if not __have_uv_and_cue
        __test_record_pass "orch run vm dry-run (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "vm-claude": schema.#Profile & {
    agent:       "claude"
    environment: "vm"
    isolation:   presets.#ClaudeCode
    "on-exit":   ["destroy-vm"]
}' > "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run vm-claude --dry-run
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "orch run vm-claude --dry-run status" $rc 0
    assert_contains "dry-run announces vm env" "$out" "env=vm"
    assert_contains "dry-run prints slop-brew-vm init" "$out" "slop-brew-vm init"
    assert_contains "dry-run prints slop-brew-vm run claude" "$out" "slop-brew-vm run claude"
end

function test_run_vm_with_credentials_dry_run_announces_copy_in
    # When a vm profile declares credentials.github != "none", the
    # orchestrator should plumb keys via `slop-brew-vm copy-in <stage>
    # ~/.ssh` between init and run. The dry-run output must surface
    # both the copy-in step in the equivalent CLI and the staging
    # explanation, so users reading dry-run output see exactly what
    # would happen.
    if not __have_uv_and_cue
        __test_record_pass "orch run vm-with-creds dry-run (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "vm-claude-creds": schema.#Profile & {
    agent:       "claude"
    environment: "vm"
    isolation:   presets.#ClaudeCode
    credentials: github: "ephemeral-rw"
    "on-exit":   ["revoke-credentials", "destroy-vm"]
}' > "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run vm-claude-creds --dry-run
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "orch run vm-with-creds --dry-run status" $rc 0
    # In dry-run, the actual stage path is not allocated (no side effects),
    # but the announcement must still mention copy-in semantics.
    assert_contains "dry-run announces vm copy-in path" "$out" "~/.ssh"
    assert_contains "dry-run announces scp transport" "$out" "scp"
end

function test_snapshot_state_writes_json_payload
    # snapshot-state on-exit hook should drop a JSON file under
    # .slop/snapshots/<utc>.json with the resolved profile + the
    # captured state. Used for post-mortem when a session goes wrong.
    if not __have_uv_and_cue
        __test_record_pass "snapshot-state writes payload (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys, json
from pathlib import Path
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator as orch

profile = orch.Profile(
    name='review',
    agent='claude',
    environment='container',
    credentials={'github': 'ephemeral-rw'},
    on_exit=['snapshot-state', 'revoke-credentials'],
    image={'extra-pip': ['ruff==0.6.0']},
)
state = orch.ProfileState(
    started_at='2026-05-06T00:00:00Z',
    credentials={'github': {'mode': 'ephemeral-rw', 'key_ids': [12345, 12346]}},
)
out = orch._snapshot_state(profile, state, Path('$tmp'), '.slop')
assert out.is_file(), out
assert out.parent == Path('$tmp/.slop/snapshots'), out.parent
data = json.loads(out.read_text())
assert data['profile']['name'] == 'review'
assert data['profile']['agent'] == 'claude'
assert data['profile']['environment'] == 'container'
assert data['profile']['credentials'] == {'github': 'ephemeral-rw'}
assert data['profile']['on_exit'] == ['snapshot-state', 'revoke-credentials']
assert data['profile']['image'] == {'extra-pip': ['ruff==0.6.0']}
assert data['state']['credentials']['github']['key_ids'] == [12345, 12346]
assert data['snapshotted_at'].endswith('Z')
print('OK snapshot:', out.name)
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "snapshot-state writes JSON payload" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "snapshot-state writes JSON payload to .slop/snapshots/"
end

function test_snapshot_state_runs_before_destructive_hooks
    # If snapshot-state appears AFTER revoke-credentials in the user's
    # on_exit list, we still want it to run first — by then the keys
    # have been revoked and the snapshot would lose context. The
    # orchestrator hoists snapshot-state to run before destructive
    # hooks regardless of declaration order.
    if not __have_uv_and_cue
        __test_record_pass "snapshot-state hoist (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys
from pathlib import Path
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator as orch

# Trace order of side effects: snapshot file created vs revoke-credentials
# fish call. snapshot-state should always come first.
events = []

class FakeProc:
    returncode = 0
    stdout = ''
    stderr = ''
def fake_run(script, *cmd):
    events.append(('fish_run', script.name, list(cmd)))
    return FakeProc()
orch._fish_run = fake_run

orig_snapshot = orch._snapshot_state
def trace_snapshot(profile, state, repo, sd):
    events.append(('snapshot',))
    return orig_snapshot(profile, state, repo, sd)
orch._snapshot_state = trace_snapshot

profile = orch.Profile(
    name='back',
    agent='claude',
    environment='host',
    credentials={'github': 'ephemeral-rw'},
    # snapshot-state declared LAST — should still hoist to first.
    on_exit=['revoke-credentials', 'stop-proxy', 'snapshot-state'],
)
state = orch.ProfileState(started_at='now', credentials={'github': {'key_ids': [1]}})
orch._on_exit_hooks(profile, state, repo_root=Path('$tmp'), state_dir='.slop')

snap_idx   = next(i for i, e in enumerate(events) if e[0] == 'snapshot')
revoke_idx = next(i for i, e in enumerate(events) if e[0] == 'fish_run' and 'revoke' in e[2])
assert snap_idx < revoke_idx, (snap_idx, revoke_idx, events)
print('OK hoist:', events[:5])
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "snapshot hoists before revoke" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "snapshot-state hoists before destructive hooks"
end

function test_create_pair_id_parser_extracts_two_ids
    # The fish gh/forgejo create-pair commands print "Created <access>
    # deploy key" then "  id: <num>" per access mode. The orchestrator
    # captures these so on-exit revoke can target them by id (rather
    # than waiting for the 24h TTL via revoke-expired --yes).
    if not __have_uv_and_cue
        __test_record_pass "create-pair id parser (skipped: uv/cue missing)"
        return 0
    end
    set -l py "
import sys
sys.path.insert(0, 'scripts/_py')
from slop_orchestrator import _parse_create_pair_ids
sample = '''Created ro deploy key
  repo: owner/repo
  id: 12345
  title: llm-agent:ro:auto-abc-20260506:exp=2026-05-07T00:00:00Z
  private key: /home/u/.ssh/llm_agent_github_ro_auto-abc_20260506T000000Z
  public key:  /home/u/.ssh/llm_agent_github_ro_auto-abc_20260506T000000Z.pub
Created rw deploy key
  repo: owner/repo
  id: 12346
  title: llm-agent:rw:auto-abc-20260506:exp=2026-05-07T00:00:00Z
'''
ids = _parse_create_pair_ids(sample)
assert ids == [12345, 12346], ids
empty = _parse_create_pair_ids('')
assert empty == [], empty
# Decoy: the parser must not match 'commit-id', 'request-id', etc.
noisy = '''Created ro deploy key
  commit-id: abc123
  id: 999
  request-id: 42
'''
ids2 = _parse_create_pair_ids(noisy)
assert ids2 == [999], ids2
print('OK parser')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "create-pair id parser" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "create-pair id parser extracts the two ids"
end

function test_revoke_credentials_uses_captured_ids
    # End-to-end: stub slop-gh-key.fish on PATH so the orchestrator's
    # _revoke_credentials function shells into our recorder. Construct
    # a ProfileState with two captured ids and confirm _revoke_credentials
    # invokes `here revoke <id>` once per id, not `here cleanup`.
    if not __have_uv_and_cue
        __test_record_pass "revoke uses captured ids (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys, os, json
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, 'scripts/_py')
import slop_orchestrator as orch

# Capture every _fish_run call rather than spawning real fish.
calls = []
class FakeProc:
    returncode = 0
    stdout = ''
    stderr = ''
def fake(script, *cmd):
    calls.append((script.name, list(cmd)))
    return FakeProc()
orch._fish_run = fake

state = orch.ProfileState(
    started_at='2026-05-06T00:00:00Z',
    credentials={
        'github':  {'mode': 'ephemeral-rw', 'key_ids': [12345, 12346]},
        'forgejo': {'mode': 'ephemeral-rw', 'key_ids': [99]},
    },
)
orch._revoke_credentials(state)
gh_calls  = [c for c in calls if c[0] == 'slop-gh-key.fish']
fj_calls  = [c for c in calls if c[0] == 'slop-forgejo-key.fish']
assert gh_calls == [
    ('slop-gh-key.fish', ['here', 'revoke', '12345']),
    ('slop-gh-key.fish', ['here', 'revoke', '12346']),
], gh_calls
assert fj_calls == [
    ('slop-forgejo-key.fish', ['here', 'revoke', '99']),
], fj_calls
# No call to `here cleanup` should have happened — that would be the
# old expired-only behavior.
assert not any('cleanup' in c[1] for c in calls), calls
print('OK revoke-by-id')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "revoke uses captured ids" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "revoke calls `here revoke <id>` per captured id"
end

function test_revoke_credentials_falls_back_to_cleanup_when_no_ids
    # State files written by older orchestrator versions (or hand-edited
    # ones) don't carry key_ids. The fallback must still invoke `here
    # cleanup` so something happens — the user's keys would just live
    # to their TTL otherwise.
    if not __have_uv_and_cue
        __test_record_pass "revoke fallback (skipped: uv/cue missing)"
        return 0
    end
    set -l py "
import sys
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator as orch

calls = []
class FakeProc:
    returncode = 0
    stdout = ''
    stderr = ''
def fake(script, *cmd):
    calls.append((script.name, list(cmd)))
    return FakeProc()
orch._fish_run = fake

state = orch.ProfileState(
    started_at='2026-05-06T00:00:00Z',
    credentials={
        'github':  {'mode': 'ephemeral-rw'},      # no key_ids
        'forgejo': {'mode': 'ephemeral-rw'},      # no key_ids
    },
)
orch._revoke_credentials(state)
assert ('slop-gh-key.fish', ['here', 'cleanup']) in calls, calls
assert ('slop-forgejo-key.fish', ['here', 'cleanup']) in calls, calls
print('OK fallback to cleanup')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "revoke fallback to cleanup" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "revoke falls back to here cleanup when no captured ids"
end

function test_credential_staging_copies_only_ephemeral_keys
    # When a container profile declares credentials.github != "none",
    # the orchestrator should stage llm_agent_github_{ro,rw}_* into
    # <state-dir>/runtime/<profile>/.ssh/ along with a fresh SSH config
    # using the staged filenames. The user's permanent identities
    # (id_ed25519 etc.) must NOT be copied — that would defeat the
    # whole point of staging instead of mounting ~/.ssh/ wholesale.
    if not __have_uv_and_cue
        __test_record_pass "credential staging (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l fake_ssh "$tmp/fake-ssh"
    mkdir -p "$fake_ssh"
    chmod 700 "$fake_ssh"
    # Real-shape filenames so the orchestrator's glob matches.
    set -l ro_name "llm_agent_github_ro_session-1_20260506T010000Z"
    set -l rw_name "llm_agent_github_rw_session-1_20260506T010000Z"
    echo "fake-ro-priv"  > "$fake_ssh/$ro_name"
    echo "fake-ro-pub"   > "$fake_ssh/$ro_name.pub"
    echo "fake-rw-priv"  > "$fake_ssh/$rw_name"
    echo "fake-rw-pub"   > "$fake_ssh/$rw_name.pub"
    # Decoy: a permanent identity that must NOT be copied.
    echo "PERMANENT-DO-NOT-COPY" > "$fake_ssh/id_ed25519"
    chmod 600 "$fake_ssh/$ro_name" "$fake_ssh/$rw_name" "$fake_ssh/id_ed25519"
    set -l py "
import sys, os, json
from pathlib import Path
os.environ['HOME'] = '$tmp'   # so Path.home() points at our fake home
# Symlink the fake-ssh into ~/.ssh so the helper finds it.
home = Path('$tmp')
ssh = home / '.ssh'
if ssh.exists():
    import shutil; shutil.rmtree(ssh)
os.symlink('$fake_ssh', ssh)
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator
profile = slop_orchestrator.Profile(
    name='review',
    agent='claude',
    environment='container',
    credentials={'github': 'ephemeral-rw'},
)
stage = slop_orchestrator._stage_credentials(profile, Path('$tmp'), '.slop')
assert stage is not None, 'stage returned None'
assert stage.is_dir(), f'stage not a dir: {stage}'
files = sorted(p.name for p in stage.iterdir())
print('staged files:', files)
assert '$ro_name'        in files, files
assert '$rw_name'        in files, files
assert '$ro_name.pub'    in files, files
assert '$rw_name.pub'    in files, files
assert 'config'          in files, files
assert 'id_ed25519'  not in files, 'PERMANENT key was copied!'
config = (stage / 'config').read_text()
assert 'Host github-llm-ro' in config
assert 'Host github-llm-rw' in config
assert '$ro_name' in config
assert '$rw_name' in config
assert 'IdentitiesOnly yes' in config
print('OK staging copied only ephemeral keys')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "credential staging filters out permanent keys" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "credential staging filters out permanent keys"
    assert_contains "stage produced expected file list" "$out" "config"
    assert_not_contains "stage did NOT include id_ed25519" "$out" "PERMANENT"
end

function test_credential_staging_handles_radicle_keypair
    # Radicle uses a single-key-per-identity model (no ro/rw split) and
    # has no SSH `Host` alias. The orchestrator stages the keypair
    # alongside any github/forgejo files but writes a comment in the
    # config telling the user the in-container path so they can wire
    # rad up themselves (we don't configure rad's own state — that's
    # rad-CLI-version-specific and varies).
    if not __have_uv_and_cue
        __test_record_pass "radicle staging (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l fake_ssh "$tmp/fake-ssh"
    mkdir -p "$fake_ssh"
    chmod 700 "$fake_ssh"
    set -l rad_name "llm_agent_radicle_session-1_20260506T040000Z"
    echo "fake-rad-priv" > "$fake_ssh/$rad_name"
    echo "fake-rad-pub"  > "$fake_ssh/$rad_name.pub"
    chmod 600 "$fake_ssh/$rad_name"
    # Decoy permanent identity that must NOT be copied.
    echo "PERMANENT-NOPE" > "$fake_ssh/id_ed25519"
    chmod 600 "$fake_ssh/id_ed25519"
    set -l py "
import sys, os
from pathlib import Path
os.environ['HOME'] = '$tmp'
home = Path('$tmp')
ssh = home / '.ssh'
if ssh.exists():
    import shutil; shutil.rmtree(ssh)
os.symlink('$fake_ssh', ssh)
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator
profile = slop_orchestrator.Profile(
    name='radicle-only',
    agent='claude',
    environment='container',
    credentials={'radicle': 'ephemeral'},
)
stage = slop_orchestrator._stage_credentials(profile, Path('$tmp'), '.slop')
assert stage is not None, 'stage returned None for radicle-only profile'
files = sorted(p.name for p in stage.iterdir())
assert '$rad_name'        in files, files
assert '$rad_name.pub'    in files, files
assert 'config'           in files, files
assert 'id_ed25519'   not in files, 'PERMANENT key was copied!'
config = (stage / 'config').read_text()
# No SSH Host stanza for radicle — just a comment pointing at the
# in-container key path.
assert 'Host github-llm' not in config
assert 'Host forgejo-llm' not in config
assert 'RAD_KEYS_PATH' in config, config
assert '$rad_name' in config, config
print('OK radicle staging')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "radicle staging" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "radicle staging copies keypair + writes RAD_KEYS_PATH hint"
end

function test_credential_staging_handles_forgejo_with_real_hostname
    # Same shape as the github test, plus the wrinkle that forgejo's
    # HostName must come from the user's existing ~/.ssh/config marker
    # block (Codeberg vs self-hosted vs Gitea — varies). The staged
    # config file's `HostName` line must match what the marker block
    # in the host config declared, NOT a hardcoded fallback.
    if not __have_uv_and_cue
        __test_record_pass "forgejo staging (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l fake_ssh "$tmp/fake-ssh"
    mkdir -p "$fake_ssh"
    chmod 700 "$fake_ssh"
    set -l ro_name "llm_agent_forgejo_ro_session-1_20260506T020000Z"
    set -l rw_name "llm_agent_forgejo_rw_session-1_20260506T020000Z"
    echo "fake-ro-priv" > "$fake_ssh/$ro_name"
    echo "fake-ro-pub"  > "$fake_ssh/$ro_name.pub"
    echo "fake-rw-priv" > "$fake_ssh/$rw_name"
    echo "fake-rw-pub"  > "$fake_ssh/$rw_name.pub"
    chmod 600 "$fake_ssh/$ro_name" "$fake_ssh/$rw_name"
    # The marker block format `slop-forgejo-key here create-pair
    # --install-ssh-config` writes when the user runs it on the host:
    echo '# BEGIN slop-forgejo-key:owner-repo:session-1:20260506T020000Z
Host forgejo-llm-ro
  HostName codeberg.example.org
  User git
  IdentityFile '"$fake_ssh/$ro_name"'
  IdentitiesOnly yes

Host forgejo-llm-rw
  HostName codeberg.example.org
  User git
  IdentityFile '"$fake_ssh/$rw_name"'
  IdentitiesOnly yes
# END slop-forgejo-key:owner-repo:session-1:20260506T020000Z
' > "$fake_ssh/config"
    set -l py "
import sys, os
from pathlib import Path
os.environ['HOME'] = '$tmp'
home = Path('$tmp')
ssh = home / '.ssh'
if ssh.exists():
    import shutil; shutil.rmtree(ssh)
os.symlink('$fake_ssh', ssh)
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator
profile = slop_orchestrator.Profile(
    name='codeberg-session',
    agent='claude',
    environment='container',
    credentials={'forgejo': 'ephemeral-rw'},
)
stage = slop_orchestrator._stage_credentials(profile, Path('$tmp'), '.slop')
assert stage is not None
files = sorted(p.name for p in stage.iterdir())
assert '$ro_name' in files, files
assert '$rw_name' in files, files
config = (stage / 'config').read_text()
assert 'Host forgejo-llm-ro' in config
assert 'Host forgejo-llm-rw' in config
assert 'codeberg.example.org' in config, 'forgejo HostName missing'
assert 'github.com'   not in config, 'github HostName leaked'
print('OK forgejo staging picked HostName from marker block')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "forgejo staging picks HostName" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "forgejo staging picks HostName from marker block"
end

function test_credential_staging_skips_forgejo_with_no_marker
    # If the user requested credentials.forgejo but never installed
    # the marker block (e.g. ran create-pair with --no-install-config),
    # we don't have a HostName to write — must skip cleanly with a
    # warning rather than guessing.
    if not __have_uv_and_cue
        __test_record_pass "forgejo no-marker skip (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l fake_ssh "$tmp/fake-ssh"
    mkdir -p "$fake_ssh"
    chmod 700 "$fake_ssh"
    set -l ro_name "llm_agent_forgejo_ro_session-1_20260506T030000Z"
    set -l rw_name "llm_agent_forgejo_rw_session-1_20260506T030000Z"
    for n in $ro_name $rw_name
        echo "fake" > "$fake_ssh/$n"
        echo "fake-pub" > "$fake_ssh/$n.pub"
        chmod 600 "$fake_ssh/$n"
    end
    # No ~/.ssh/config at all → no marker block → forgejo skipped.
    set -l py "
import sys, os
from pathlib import Path
os.environ['HOME'] = '$tmp'
home = Path('$tmp')
ssh = home / '.ssh'
if ssh.exists():
    import shutil; shutil.rmtree(ssh)
os.symlink('$fake_ssh', ssh)
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator
profile = slop_orchestrator.Profile(
    name='no-config',
    agent='claude',
    environment='container',
    credentials={'forgejo': 'ephemeral-rw'},
)
stage = slop_orchestrator._stage_credentials(profile, Path('$tmp'), '.slop')
# With no other family asking for staging and forgejo skipped, the
# helper returns None (no .ssh/ to mount).
assert stage is None, f'expected None when forgejo cant be staged; got {stage}'
print('OK no-marker → no stage')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "forgejo no-marker skip" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "forgejo skips when no marker block"
end

function test_image_spec_hash_is_order_insensitive_for_packages
    # Two profiles that ask for the same packages in a different order
    # must hash to the same tag (so the docker build cache hits). The
    # base tag is asymmetric — different bases must produce different
    # hashes even with identical packages.
    if not __have_uv_and_cue
        __test_record_pass "image hash determinism (skipped: uv/cue missing)"
        return 0
    end
    set -l py "
import sys
sys.path.insert(0, 'scripts/_py')
from slop_orchestrator import _image_spec_hash, DEFAULT_IMAGE_BASE
a = {'extra-pip': ['ruff==0.6.0', 'mypy==1.10.0'], 'extra-npm': ['gh@2.0.0']}
b = {'extra-pip': ['mypy==1.10.0', 'ruff==0.6.0'], 'extra-npm': ['gh@2.0.0']}
assert _image_spec_hash(a, DEFAULT_IMAGE_BASE) == _image_spec_hash(b, DEFAULT_IMAGE_BASE), 'list order should not matter'
empty = {}
assert _image_spec_hash(a, DEFAULT_IMAGE_BASE) != _image_spec_hash(empty, DEFAULT_IMAGE_BASE)
# Asymmetric: base tag is part of the hash.
assert _image_spec_hash(a, 'other:tag') != _image_spec_hash(a, DEFAULT_IMAGE_BASE), 'base tag should change hash'
print('OK hash deterministic + base-sensitive')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "image hash determinism" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "image hash is order-insensitive + base-sensitive"
end

function test_render_tailored_dockerfile_layers_apt_pip_npm
    if not __have_uv_and_cue
        __test_record_pass "render tailored dockerfile (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys
from pathlib import Path
sys.path.insert(0, 'scripts/_py')
from slop_orchestrator import _render_tailored_dockerfile
target = Path('$tmp/Dockerfile.tailored')
spec = {
    'extra-apt': ['ripgrep', 'jq'],
    'extra-pip': ['ruff==0.6.0'],
    'extra-npm': ['gh@2.0.0'],
}
_render_tailored_dockerfile(target, 'local/agent-sandbox-tools:latest', spec)
text = target.read_text()
assert 'FROM local/agent-sandbox-tools:latest' in text, text
assert 'apt-get install -y --no-install-recommends ripgrep jq' in text, text
assert 'uv pip install --system --no-cache ruff==0.6.0' in text, text
assert 'npm install -g gh@2.0.0' in text, text
# Empty spec should produce just FROM.
target2 = Path('$tmp/Dockerfile.empty')
_render_tailored_dockerfile(target2, 'local/agent-sandbox-tools:latest', {})
empty_text = target2.read_text()
assert empty_text.strip() == 'FROM local/agent-sandbox-tools:latest', empty_text
print('OK dockerfile')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "render tailored dockerfile" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "tailored dockerfile layers apt + pip + npm"
end

function test_resolve_image_tag_returns_none_when_no_spec
    # Plain profile without image: → orchestrator stays on the default
    # local/agent-sandbox-tools:latest path (no override needed).
    if not __have_uv_and_cue
        __test_record_pass "resolve no-spec (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys
from pathlib import Path
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator as orch
profile = orch.Profile(name='no-image', agent='claude', environment='container')
tag, dockerfile = orch._resolve_image_tag(profile, Path('$tmp'), '.slop')
assert tag is None, tag
assert dockerfile is None, dockerfile
print('OK no-spec → (None, None)')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "resolve no-spec" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "resolve_image_tag returns None when no spec"
end

function test_resolve_image_tag_returns_tailored_with_extras
    if not __have_uv_and_cue
        __test_record_pass "resolve tailored (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys
from pathlib import Path
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator as orch
profile = orch.Profile(
    name='custom',
    agent='claude',
    environment='container',
    image={'extra-pip': ['ruff==0.6.0']},
)
tag, dockerfile = orch._resolve_image_tag(profile, Path('$tmp'), '.slop')
assert tag is not None and tag.startswith('local/agent-sandbox-tools:slop-'), tag
assert dockerfile is not None
assert 'runtime/custom' in str(dockerfile), dockerfile
print('OK tailored:', tag)
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "resolve tailored" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "resolve_image_tag returns tailored tag for extras"
end

function test_run_tailored_image_dry_run_announces_build
    # Container profile with image.extra-pip → dry-run output mentions
    # the would-build tailored tag and the FROM-base. Catches the case
    # where image extras are silently dropped.
    if not __have_uv_and_cue
        __test_record_pass "tailored dry-run (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "fancy": schema.#Profile & {
    agent:       "claude"
    environment: "container"
    isolation:   presets.#ClaudeCode
    image: {
        "extra-pip": ["ruff==0.6.0"]
        "extra-apt": ["ripgrep"]
    }
}' > "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run fancy --dry-run
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "dry-run fancy status" $rc 0
    assert_contains "dry-run announces tailored tag" "$out" "local/agent-sandbox-tools:slop-"
    assert_contains "dry-run announces base FROM" "$out" "FROM local/agent-sandbox-tools:latest"
    assert_contains "dry-run announces apt extras" "$out" "ripgrep"
    assert_contains "dry-run announces pip extras" "$out" "ruff==0.6.0"
end

function test_compose_override_renders_bind_mount
    # The override file must add a read-only volume mapping the staged
    # .ssh/ to /root/.ssh in the agent-tools service. If the path or
    # mode regresses, agents inside the container won't see the keys.
    if not __have_uv_and_cue
        __test_record_pass "compose override (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/stage/.ssh"
    set -l py "
import sys
from pathlib import Path
sys.path.insert(0, 'scripts/_py')
import slop_orchestrator
override = slop_orchestrator._render_compose_override(Path('$tmp/stage/.ssh'))
assert override.is_file(), f'override not written: {override}'
text = override.read_text()
assert 'agent-tools' in text
assert '/root/.ssh:ro' in text
assert '$tmp/stage/.ssh' in text or '$tmp' in text
print('override OK')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "compose override renders" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "compose override renders bind mount"
end

function test_dry_run_announces_credential_mount
    # The container dry-run should mention that credentials WOULD be
    # staged when a profile asks for them. Without that, a user
    # reading the dry-run output has no way to tell the credential
    # plumbing is on.
    if not __have_uv_and_cue
        __test_record_pass "dry-run announces creds (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run review --dry-run
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "dry-run review status" $rc 0
    assert_contains "dry-run mentions staging" "$out" "stage"
    assert_contains "dry-run names the in-container mount path" "$out" "/root/.ssh"
end

function test_run_unknown_environment_still_rejects
    # The closed disjunction in the schema (host|container|vm) prevents
    # bogus environments from validating, but if someone bypasses cue
    # and pokes the JSON directly the orchestrator must still fail
    # cleanly. Build a slop.cue with an environment cue rejects, then
    # confirm we get the schema-level rejection (cue's job) rather
    # than the orchestrator silently launching nothing.
    if not __have_uv_and_cue
        __test_record_pass "orch run unknown env rejected (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "weird": schema.#Profile & {
    agent:       "claude"
    environment: "kubernetes"
    isolation:   presets.#ClaudeCode
}' > "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run weird
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "orch run unknown-env fails" $rc 1
    assert_contains "error mentions kubernetes (cue rejection)" "$out" "kubernetes"
end

function test_down_with_no_state_is_a_no_op
    if not __have_uv_and_cue
        __test_record_pass "orch down no-state (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' down
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_status "orch down with no state status" $rc 0
    assert_contains "down reports no active profiles" "$out" "no active profiles"
end

# ---------------------------------------------------------------------------
# slop.fish dispatch
# ---------------------------------------------------------------------------

function test_fish_help_advertises_orchestrator_subcommands
    set -l out (run_fish "$SLOP_FISH" help 2>&1)
    set -l rc $status
    assert_status "slop help status" $rc 0
    for sub in 'slop run' 'slop validate' 'slop list' 'slop down'
        assert_contains "slop help mentions: $sub" "$out" "$sub"
    end
end

function test_fish_dispatch_routes_validate_to_orchestrator
    # When the user types `slop validate` from a directory without
    # slop.cue, the orchestrator must be the thing that errors. The
    # signal is the string "slop.cue" in the error (the orchestrator's
    # message; the Textual TUI never says that).
    if not __have_uv_and_cue
        __test_record_pass "slop validate dispatch (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l out (__slop_in "$tmp" validate)
    set -l rc $status
    assert_eq "slop validate fails when no slop.cue" $rc 1
    assert_contains "slop validate error names slop.cue" "$out" "slop.cue"
end

function test_tui_no_slop_cue_shows_pointer_to_sample
    # When the user opens the TUI from a directory that has no slop.cue,
    # the "Run profile" submenu must still exist and offer a clear hint
    # — pointer at the bundled sample. Inspect build_top_actions()
    # programmatically rather than driving the TUI; faster + deterministic.
    if not __have_uv_and_cue
        __test_record_pass "TUI no-cue submenu (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l py "
import sys, os
os.environ['ATB_USER_PWD'] = '$tmp'
sys.path.insert(0, 'scripts/_py')
import slop_tui
top = slop_tui.build_top_actions()
run_action = next((a for a in top if a.key == 'p'), None)
assert run_action is not None, 'no key=p Run profile action'
assert run_action.submenu is not None, 'Run profile has no submenu'
labels = [a.label for a in run_action.submenu]
assert any('no slop.cue' in lbl for lbl in labels), labels
print('OK no-cue:', labels)
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet --with 'textual>=0.79' python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "TUI no-cue submenu" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "TUI no-cue submenu shows sample pointer"
end

function test_tui_with_slop_cue_lists_profiles_in_submenu
    # The bundled sample has two profiles. When ATB_USER_PWD points at
    # a dir containing it, the Run-profile submenu must list both,
    # plus the validate / list / down helper rows.
    if not __have_uv_and_cue
        __test_record_pass "TUI with-cue submenu (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l py "
import sys, os
os.environ['ATB_USER_PWD'] = '$tmp'
sys.path.insert(0, 'scripts/_py')
import slop_tui
top = slop_tui.build_top_actions()
run_action = next(a for a in top if a.key == 'p')
labels = [a.label for a in run_action.submenu]
clis   = [a.equivalent_cli for a in run_action.submenu]
joined = ' | '.join(labels)
assert 'review' in joined, joined
assert 'explore' in joined, joined
assert any('agent=claude'  in lbl for lbl in labels), labels
assert any('agent=opencode' in lbl for lbl in labels), labels
assert 'slop run review'  in clis, clis
assert 'slop run explore' in clis, clis
assert 'slop validate' in clis, clis
assert 'slop list'     in clis, clis
assert 'slop down'     in clis, clis
print('OK with-cue: profiles + helpers all present')
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet --with 'textual>=0.79' python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "TUI with-cue submenu" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "TUI with-cue submenu lists profiles + helpers"
end

function test_tui_default_profile_marked_with_star
    # The orchestrator's `list` marks the default profile with `* `;
    # the TUI uses a star (★) in the label. Either is fine — the test
    # just checks the default profile's label has SOMETHING the
    # non-default lacks, so users can tell at a glance.
    if not __have_uv_and_cue
        __test_record_pass "TUI marks default (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l py "
import sys, os
os.environ['ATB_USER_PWD'] = '$tmp'
sys.path.insert(0, 'scripts/_py')
import slop_tui
top = slop_tui.build_top_actions()
run_action = next(a for a in top if a.key == 'p')
review_label = next(a.label for a in run_action.submenu if 'review' in a.label and 'agent=' in a.label)
explore_label = next(a.label for a in run_action.submenu if 'explore' in a.label and 'agent=' in a.label)
# review is the default, explore is not
assert review_label[0] != ' ' or '★' in review_label, repr(review_label)
print('OK marked:', review_label)
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet --with 'textual>=0.79' python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "TUI default-marker visible" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "TUI default-marker visible on review"
end

function test_tui_malformed_slop_cue_does_not_crash
    # If slop.cue has a typo CUE rejects, build_top_actions() must
    # still complete (returning a fallback "validate for the error"
    # entry) so the user can still launch the TUI to see other tools.
    if not __have_uv_and_cue
        __test_record_pass "TUI tolerates broken slop.cue (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    echo 'this is not valid cue!@#$' > "$tmp/slop.cue"
    set -l py "
import sys, os
os.environ['ATB_USER_PWD'] = '$tmp'
sys.path.insert(0, 'scripts/_py')
import slop_tui
top = slop_tui.build_top_actions()
run_action = next(a for a in top if a.key == 'p')
labels = [a.label for a in run_action.submenu]
joined = ' | '.join(labels)
assert any('invalid' in lbl.lower() or 'validate' in lbl.lower() for lbl in labels), joined
print('OK fallback:', labels)
"
    set -l out (env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \
        uv run --quiet --with 'textual>=0.79' python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "TUI tolerates broken slop.cue" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "TUI tolerates broken slop.cue"
end

function test_fish_dispatch_unknown_arg_still_errors
    # The orchestrator subcommands shouldn't shadow the existing
    # "Unknown argument" error path for arbitrary other strings.
    set -l tmp (mk_tmpdir)
    set -l out (__slop_in "$tmp" not-a-real-command)
    set -l rc $status
    assert_eq "slop unknown arg fails" $rc 1
    assert_contains "slop unknown arg shows error" "$out" "unknown argument"
end

run_tests_in_file (basename (status filename))
