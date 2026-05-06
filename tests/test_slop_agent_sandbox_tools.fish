#!/usr/bin/env fish

# Tests for scripts/slop-agent-sandbox-tools.fish — same surface as slop-agent-sandbox.

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-agent-sandbox-tools.fish"

function __invoke
    command fish -c "source '$SCRIPT'; slop-agent-sandbox-tools $argv" 2>&1
end

function test_help_subcommand
    set -l out (__invoke help)
    set -l rc $status
    assert_status "slop-agent-sandbox-tools help status" $rc 0
    assert_contains "slop-agent-sandbox-tools help mentions Usage" "$out" "Usage:"
end

function test_no_args_prints_usage
    set -l out (__invoke)
    set -l rc $status
    assert_status "slop-agent-sandbox-tools no-args status" $rc 0
    assert_contains "slop-agent-sandbox-tools no-args mentions Usage" "$out" "Usage:"
end

function test_unknown_command_fails
    pushd "$REPO_ROOT" >/dev/null
    set -l out (__invoke not-a-real-command)
    set -l rc $status
    popd >/dev/null
    assert_eq "slop-agent-sandbox-tools unknown cmd fails" $rc 1
    assert_contains "slop-agent-sandbox-tools unknown cmd message" "$out" "Unknown command"
end

function test_invalid_network_policy_rejected
    pushd "$REPO_ROOT" >/dev/null
    set -l out (__invoke run --network-policy bogus)
    set -l rc $status
    popd >/dev/null
    assert_eq "slop-agent-sandbox-tools invalid policy fails" $rc 1
    assert_contains "slop-agent-sandbox-tools invalid policy message" "$out" "Invalid --network-policy"
end

function test_missing_compose_file_reported
    set -l tmp (mk_tmpdir)
    pushd $tmp >/dev/null
    set -l out (__invoke run)
    set -l rc $status
    popd >/dev/null
    assert_eq "slop-agent-sandbox-tools missing compose fails" $rc 1
    assert_contains "slop-agent-sandbox-tools missing compose message" "$out" "docker-compose.yml"
end

function test_help_advertises_tui_and_examples
    set -l out (__invoke help)
    assert_contains "slop-agent-sandbox-tools help mentions tui" "$out" "slop-agent-sandbox-tools tui"
    assert_contains "slop-agent-sandbox-tools help mentions Examples" "$out" "Examples"
end

function test_build_includes_base_agent_for_from_dependency
    # Regression: Dockerfile.agent.tools begins with
    #   FROM local/agent-sandbox:latest
    # so the `agent` service (which produces that tag) must be built
    # before `agent-tools`. `docker compose build` does NOT walk the
    # FROM-dependency chain across services, so building only
    # `agent-tools` failed with a confusing "pull access denied" from
    # docker.io as if it were a registry-auth issue. Fix is to list both
    # services in every build invocation. Static check: every `compose_cmd
    # ... build` line in the script must build `agent` alongside
    # `agent-tools`, never `agent-tools` alone.
    set -l content (cat "$SCRIPT")
    # Catch any line of the form `compose_cmd ... build agent-tools` that
    # is NOT also building `agent`. Use grep -E so the pattern is readable.
    set -l bad (grep -nE 'compose_cmd .* build agent-tools$' "$SCRIPT")
    if test -n "$bad"
        __test_record_fail "every build includes the base 'agent' service" \
            "lone agent-tools build: $bad"
        return
    end
    __test_record_pass "every build includes the base 'agent' service"
    # Sanity check: the rewritten lines explicitly mention both services.
    set -l count (grep -cE 'compose_cmd .* build agent agent-tools' "$SCRIPT")
    if test "$count" -lt 3
        __test_record_fail "build agent agent-tools appears for run/shell/up" \
            "expected ≥3 occurrences, got $count"
    else
        __test_record_pass "build agent agent-tools appears for run/shell/up"
    end
    # Confirm Dockerfile.agent.tools really has the FROM-dep — if someone
    # rewrites it to a plain alpine base, this test becomes meaningless
    # and should be removed.
    set -l df "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools"
    if test -f "$df"
        set -l dfc (cat "$df")
        assert_contains "Dockerfile.agent.tools still depends on local/agent-sandbox" \
            "$dfc" "FROM local/agent-sandbox"
    end
end

function test_tui_without_gum_prints_install_hint
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    set -l body "
        set -x PATH '$tmp/bin'
        source '$SCRIPT'
        slop-agent-sandbox-tools tui
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-agent-sandbox-tools tui no-gum fails" $rc 1
    assert_contains "slop-agent-sandbox-tools tui no-gum mentions gum" "$out" "gum"
    assert_contains "slop-agent-sandbox-tools tui no-gum suggests brew install" "$out" "brew install gum"
end

run_tests_in_file (basename (status filename))
