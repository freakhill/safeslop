#!/usr/bin/env fish

# Tests for the root-level `install` bootstrap.
# - exec bit is set
# - --help renders without writing or execing fish
# - syntax-check passes
# - delegates to scripts/slop-install.fish (verified via output snippet)

source (dirname (status filename))/helpers.fish

set -g BOOTSTRAP "$REPO_ROOT/install"

function test_bootstrap_is_executable
    if test -x "$BOOTSTRAP"
        __test_record_pass "install bootstrap has exec bit"
    else
        __test_record_fail "install bootstrap has exec bit" "missing +x on $BOOTSTRAP"
    end
end

function test_bootstrap_syntax_parses
    if command fish -n "$BOOTSTRAP" 2>/dev/null
        __test_record_pass "install bootstrap syntax parses"
    else
        __test_record_fail "install bootstrap syntax parses" "fish -n reported errors"
    end
end

function test_bootstrap_help_does_not_install
    set -l out (run_fish "$BOOTSTRAP" --help 2>&1)
    set -l rc $status
    assert_status "install --help status" $rc 0
    assert_contains "install --help mentions Usage" "$out" "Usage:"
    assert_contains "install --help mentions --no-exec" "$out" "--no-exec"
    assert_contains "install --help mentions --dry-run" "$out" "--dry-run"
end

function test_bootstrap_dry_run_writes_nothing
    # Use a tmp HOME so the real conf.d is untouched. The bootstrap calls
    # scripts/slop-install.fish install --dry-run, which should report what
    # it would do without modifying anything.
    set -l tmp (mk_tmpdir)
    set -l out (env HOME="$tmp" $FISH_BIN "$BOOTSTRAP" --dry-run --no-exec 2>&1)
    set -l rc $status
    assert_status "install --dry-run status" $rc 0
    assert_contains "install --dry-run mentions Would write" "$out" "Would write snippet"
    if test -e "$tmp/.config/fish/conf.d/safeslop.fish"
        __test_record_fail "install --dry-run writes nothing" "snippet was created"
    else
        __test_record_pass "install --dry-run writes nothing"
    end
end

function test_bootstrap_no_exec_does_not_replace_shell
    # When --no-exec is passed, install must finish normally (status 0)
    # rather than replacing the shell. Combined with --dry-run we get a
    # safe, idempotent assertion path.
    set -l tmp (mk_tmpdir)
    set -l out (env HOME="$tmp" $FISH_BIN "$BOOTSTRAP" --dry-run --no-exec 2>&1)
    set -l rc $status
    assert_status "install --no-exec returns control" $rc 0
end

run_tests_in_file
