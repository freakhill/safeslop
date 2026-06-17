#!/usr/bin/env fish

# Tests for scripts/slop-install.fish
# - help paths
# - install/uninstall/status against an isolated tmp conf-dir
# - generated snippet parses as valid fish
# - sourced snippet exposes both module functions and standalone wrappers
# - cleanup honors --no-cleanup; legacy paths in $HOME are not touched in CI

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-install.fish"

function test_help_subcommand
    set -l out (run_fish $SCRIPT help 2>&1)
    set -l rc $status
    assert_status "slop-install help status" $rc 0
    assert_contains "slop-install help mentions Usage" "$out" "Usage:"
    assert_contains "slop-install help mentions install" "$out" "install"
    assert_contains "slop-install help mentions uninstall" "$out" "uninstall"
end

function test_dash_dash_help
    set -l out (run_fish $SCRIPT --help 2>&1)
    set -l rc $status
    assert_status "slop-install --help status" $rc 0
    assert_contains "slop-install --help mentions Usage" "$out" "Usage:"
end

function test_help_includes_enriched_sections
    set -l out (run_fish $SCRIPT help 2>&1)
    assert_contains "slop-install help has Description" "$out" "Description:"
    assert_contains "slop-install help has Examples" "$out" "Examples"
end

function test_unknown_argument_fails
    set -l out (run_fish $SCRIPT --bogus-flag 2>&1)
    set -l rc $status
    assert_eq "slop-install unknown arg fails" $rc 1
    assert_contains "slop-install unknown arg message" "$out" "Unknown argument"
end

function test_conf_dir_must_be_absolute
    set -l out (run_fish $SCRIPT install --conf-dir ./relative 2>&1)
    set -l rc $status
    assert_eq "slop-install relative conf-dir fails" $rc 1
    assert_contains "slop-install relative conf-dir message" "$out" "absolute"
end

function test_conf_dir_requires_value
    set -l out (run_fish $SCRIPT install --conf-dir 2>&1)
    set -l rc $status
    assert_eq "slop-install --conf-dir without value fails" $rc 1
    assert_contains "slop-install --conf-dir msg" "$out" "requires a value"
end

function test_status_does_not_modify_target
    # status should be read-only; running it against an empty conf-dir should
    # not write anything.
    set -l tmp (mk_tmpdir)
    set -l before (find $tmp -mindepth 1 2>/dev/null | wc -l | string trim)
    set -l out (run_fish $SCRIPT status --conf-dir $tmp 2>&1)
    set -l rc $status
    set -l after (find $tmp -mindepth 1 2>/dev/null | wc -l | string trim)
    assert_status "slop-install status status" $rc 0
    assert_eq "slop-install status did not write" "$after" "$before"
    assert_contains "slop-install status reports not installed" "$out" "not installed"
end

function test_install_dry_run_writes_nothing
    set -l tmp (mk_tmpdir)
    set -l out (run_fish $SCRIPT install --conf-dir $tmp --dry-run --no-cleanup 2>&1)
    set -l rc $status
    set -l snippet "$tmp/safeslop.fish"
    assert_status "install --dry-run status" $rc 0
    if test -e "$snippet"
        __test_record_fail "install --dry-run wrote nothing" "snippet was created"
    else
        __test_record_pass "install --dry-run wrote nothing"
    end
    assert_contains "install --dry-run mentions Would write" "$out" "Would write snippet"
end

function test_install_creates_managed_snippet
    set -l tmp (mk_tmpdir)
    run_fish $SCRIPT install --conf-dir $tmp --no-cleanup >/dev/null
    set -l snippet "$tmp/safeslop.fish"
    if not test -f "$snippet"
        __test_record_fail "install creates snippet" "snippet missing"
        return
    end
    __test_record_pass "install creates snippet"
    set -l content (cat $snippet)
    assert_contains "snippet has marker" "$content" "managed-by: safeslop/slop-install"
    assert_contains "snippet sets ATB_REPO_ROOT" "$content" "ATB_REPO_ROOT"
    assert_contains "snippet wraps slop-sandboxctl" "$content" "function slop-sandboxctl"
    assert_contains "snippet wraps slop" "$content" "function slop"
    assert_contains "snippet sources module loop" "$content" "for __atb_m in"
    assert_contains "snippet sources completions" "$content" "scripts/completions"
end

function test_generated_snippet_parses_as_fish
    set -l tmp (mk_tmpdir)
    run_fish $SCRIPT install --conf-dir $tmp --no-cleanup >/dev/null
    if command fish -n "$tmp/safeslop.fish" 2>/dev/null
        __test_record_pass "generated snippet parses as fish"
    else
        __test_record_fail "generated snippet parses as fish" "fish -n reported errors"
    end
end

function test_sourced_snippet_exposes_commands
    set -l tmp (mk_tmpdir)
    run_fish $SCRIPT install --conf-dir $tmp --no-cleanup >/dev/null
    set -l snippet "$tmp/safeslop.fish"

    # Module function: slop-agent-sandbox is defined inside slop-agent-sandbox.fish, so
    # sourcing the snippet should make it callable.
    set -l body "source '$snippet'; functions -q slop-agent-sandbox; and echo MODULE_OK"
    set -l out (command fish -N -c "$body" 2>&1)
    assert_contains "snippet exposes slop-agent-sandbox module function" "$out" "MODULE_OK"

    # Wrapper function: slop-sandboxctl is defined as a thin wrapper in the snippet.
    set -l body2 "source '$snippet'; functions -q slop-sandboxctl; and echo WRAPPER_OK"
    set -l out2 (command fish -N -c "$body2" 2>&1)
    assert_contains "snippet exposes slop-sandboxctl wrapper" "$out2" "WRAPPER_OK"

    # Regression: every shipped slop-* module function must be exposed by the
    # snippet. Prior to the slop- rename's tail commit, three modules were
    # named in the install list under their old llm-/safe-uv- aliases and
    # silently skipped because the files no longer exist on disk.
    for fn in slop-gh-key slop-forgejo-key slop-safe-uv slop-isolate slop-agents
        set -l body3 "source '$snippet'; functions -q $fn; and echo $fn:OK"
        set -l out3 (command fish -N -c "$body3" 2>&1)
        assert_contains "snippet exposes $fn module function" "$out3" "$fn:OK"
    end
end

function test_uninstall_removes_managed_snippet
    set -l tmp (mk_tmpdir)
    run_fish $SCRIPT install --conf-dir $tmp --no-cleanup >/dev/null
    run_fish $SCRIPT uninstall --conf-dir $tmp >/dev/null
    if test -e "$tmp/safeslop.fish"
        __test_record_fail "uninstall removed snippet" "snippet still present"
    else
        __test_record_pass "uninstall removed snippet"
    end
end

function test_uninstall_refuses_unmanaged_file
    # Drop a hand-written file with no marker; uninstall must refuse.
    set -l tmp (mk_tmpdir)
    set -l snippet "$tmp/safeslop.fish"
    echo "# user wrote this; not us" > "$snippet"
    set -l out (run_fish $SCRIPT uninstall --conf-dir $tmp 2>&1)
    set -l rc $status
    assert_eq "uninstall refuses unmanaged file" $rc 1
    assert_contains "uninstall mentions unmanaged" "$out" "unmanaged"
    if not test -f "$snippet"
        __test_record_fail "uninstall preserved unmanaged file" "file was deleted"
    else
        __test_record_pass "uninstall preserved unmanaged file"
    end
end

function test_install_no_cleanup_skips_legacy_check
    # The cleanup walks $HOME paths; --no-cleanup must not touch them.
    # We verify by asserting the install output does NOT mention "Removed
    # legacy" or "Would remove legacy", regardless of what is in $HOME.
    set -l tmp (mk_tmpdir)
    set -l out (run_fish $SCRIPT install --conf-dir $tmp --no-cleanup 2>&1)
    assert_not_contains "install --no-cleanup did not remove legacy" "$out" "Removed legacy"
    assert_not_contains "install --no-cleanup did not preview legacy" "$out" "Would remove legacy"
end

run_tests_in_file (basename (status filename))
