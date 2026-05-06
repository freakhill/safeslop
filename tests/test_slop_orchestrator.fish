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

function test_run_container_profile_rejects_with_phase_e_hint
    # The bundled sample's "review" profile is environment=container.
    # Phase D only supports host. The error message must point at
    # Phase E so users know it's intentional, not a bug.
    if not __have_uv_and_cue
        __test_record_pass "orch run container rejected (skipped: uv/cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    cp "$REPO_ROOT/library/layer/policy/samples/slop/slop.cue" "$tmp/slop.cue"
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        env UV_NATIVE_TLS=1 SSL_CERT_FILE=/etc/ssl/cert.pem \\
            uv run --script --quiet '$ORCH_PY' run review
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "orch run review (container) fails" $rc 1
    assert_contains "error mentions environment=container" "$out" "container"
    assert_contains "error mentions Phase E" "$out" "Phase E"
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
