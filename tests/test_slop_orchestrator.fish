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

function test_run_vm_profile_still_rejects_with_phase_g_hint
    # vm landed in Phase G of the plan; the orchestrator should still
    # bail with a recognizable error rather than silently passing.
    if not __have_uv_and_cue
        __test_record_pass "orch run vm rejected (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "evil": schema.#Profile & {
    agent:       "claude"
    environment: "vm"
    isolation:   presets.#ClaudeCode
}' > "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run evil
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "orch run vm fails" $rc 1
    assert_contains "error mentions Phase G" "$out" "Phase G"
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
