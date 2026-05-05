#!/usr/bin/env fish

# Tests for scripts/slop-sandboxctl.fish

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-sandboxctl.fish"

function test_no_args_prints_usage
    set -l out (run_fish $SCRIPT 2>&1)
    set -l rc $status
    assert_status "slop-sandboxctl no-args status" $rc 0
    assert_contains "slop-sandboxctl no-args mentions Usage" "$out" "Usage:"
end

function test_help_subcommand
    set -l out (run_fish $SCRIPT help 2>&1)
    set -l rc $status
    assert_status "slop-sandboxctl help status" $rc 0
    assert_contains "slop-sandboxctl help mentions Usage" "$out" "Usage:"
    assert_contains "slop-sandboxctl help lists docker" "$out" "docker"
    assert_contains "slop-sandboxctl help lists tutorial topics" "$out" "Topics:"
end

function test_help_flag
    set -l out (run_fish $SCRIPT --help 2>&1)
    set -l rc $status
    assert_status "slop-sandboxctl --help status" $rc 0
    assert_contains "slop-sandboxctl --help mentions Usage" "$out" "Usage:"
end

function test_list_subcommand
    set -l out (run_fish $SCRIPT list 2>&1)
    set -l rc $status
    assert_status "slop-sandboxctl list status" $rc 0
    assert_contains "slop-sandboxctl list shows docker mapping" "$out" "slop-agent-sandbox.fish"
    assert_contains "slop-sandboxctl list shows pinning mapping" "$out" "slop-pinning.fish"
    assert_contains "slop-sandboxctl list shows isolate mapping" "$out" "slop-isolate.fish"
end

function test_tutorial_known_topic
    for topic in docker local slop-brew-vm github-keys forgejo-keys radicle-access network-limiting file-sharing
        set -l out (run_fish $SCRIPT tutorial $topic 2>&1)
        set -l rc $status
        assert_status "slop-sandboxctl tutorial $topic status" $rc 0
        # All topic outputs are non-empty.
        if test -z "$out"
            __test_record_fail "slop-sandboxctl tutorial $topic non-empty" "no output"
        else
            __test_record_pass "slop-sandboxctl tutorial $topic non-empty"
        end
    end
end

function test_tutorial_unknown_topic_fails
    set -l out (run_fish $SCRIPT tutorial nonsense-topic 2>&1)
    set -l rc $status
    assert_eq "slop-sandboxctl tutorial unknown fails" $rc 1
    assert_contains "slop-sandboxctl tutorial unknown message" "$out" "Unknown tutorial topic"
end

function test_tutorial_missing_topic_fails
    set -l out (run_fish $SCRIPT tutorial 2>&1)
    set -l rc $status
    assert_eq "slop-sandboxctl tutorial missing topic fails" $rc 1
end

function test_unknown_command_fails
    set -l out (run_fish $SCRIPT not-a-real-command 2>&1)
    set -l rc $status
    assert_eq "slop-sandboxctl unknown cmd fails" $rc 1
    assert_contains "slop-sandboxctl unknown cmd message" "$out" "Unknown command"
end

# Note: end-to-end dispatch (e.g. `slop-sandboxctl pinning` actually running
# slop-pinning.fish from the repo root) is intentionally not tested here.
# The dispatch-target scripts use `set -l script_dir (cd ...; pwd)`, which
# silently changes the script's cwd in fish — a separate issue tracked outside
# this test suite. Tests above already cover slop-sandboxctl's own argv handling.

run_tests_in_file (basename (status filename))
