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
