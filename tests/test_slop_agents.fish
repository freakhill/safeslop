#!/usr/bin/env fish

# Tests for scripts/slop-agents.fish — one-step launchers for Claude Code
# and OpenCode that apply the repo's bundled defaults if no per-project
# override is already in place. We never invoke the real `claude` /
# `opencode` binaries — those are interactive REPLs requiring a TTY.
# Tests focus on the pure-fish surface: help text, missing-binary error
# path, seed behaviour, and the resolve_root precedence (cwd > repo root
# > nothing).

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-agents.fish"

function __invoke
    command fish -c "source '$SCRIPT'; slop-agents $argv" 2>&1
end

function test_help_subcommand
    set -l out (__invoke help)
    set -l rc $status
    assert_status "slop-agents help status" $rc 0
    assert_contains "slop-agents help mentions Usage" "$out" "Usage:"
    assert_contains "slop-agents help mentions claude" "$out" "claude"
    assert_contains "slop-agents help mentions opencode" "$out" "opencode"
    assert_contains "slop-agents help mentions seed" "$out" "seed"
    assert_contains "slop-agents help mentions Examples" "$out" "Examples"
end

function test_dash_dash_help
    set -l out (__invoke --help)
    set -l rc $status
    assert_status "slop-agents --help status" $rc 0
    assert_contains "slop-agents --help mentions Usage" "$out" "Usage:"
end

function test_no_args_prints_help
    set -l out (__invoke)
    set -l rc $status
    assert_status "slop-agents no-args status" $rc 0
    assert_contains "slop-agents no-args mentions Usage" "$out" "Usage:"
end

function test_unknown_subcommand_fails
    set -l out (__invoke not-a-real-command)
    set -l rc $status
    assert_eq "slop-agents unknown sub fails" $rc 1
    assert_contains "slop-agents unknown sub error" "$out" "unknown subcommand"
end

function test_claude_without_binary_prints_install_hint
    # Stub PATH that excludes `claude`. The launcher must fail fast with
    # an actionable npm-install hint rather than a confusing exec error.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    # Provide a minimal toolset (git for repo-root resolution, command -sq
    # uses fish builtins so PATH suffices for binary lookups).
    ln -s (command -v git) "$tmp/bin/git"
    set -l body "
        set -x PATH '$tmp/bin'
        source '$SCRIPT'
        slop-agents claude
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-agents claude no-binary fails" $rc 1
    assert_contains "slop-agents claude no-binary mentions npm install" "$out" \
        "npm install -g @anthropic-ai/claude-code"
    assert_contains "slop-agents claude no-binary suggests container fallback" \
        "$out" "slop-agent-sandbox-tools shell"
end

function test_opencode_without_binary_prints_install_hint
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    ln -s (command -v git) "$tmp/bin/git"
    set -l body "
        set -x PATH '$tmp/bin'
        source '$SCRIPT'
        slop-agents opencode
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-agents opencode no-binary fails" $rc 1
    assert_contains "slop-agents opencode no-binary mentions npm install" "$out" \
        "npm install -g opencode-ai"
end

function test_seed_claude_writes_fixture_into_fresh_repo
    # Initialize a fresh git repo, run `slop-agents seed claude` with
    # ATB_USER_PWD pointing at it, and confirm the fixture lands at
    # .claude/settings.json with the expected sandbox marker.
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        set -x ATB_USER_PWD '$tmp'
        source '$SCRIPT'
        slop-agents seed claude
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_status "slop-agents seed claude status" $rc 0
    assert_contains "seed claude reports written path" "$out" ".claude/settings.json"
    if test -f "$tmp/.claude/settings.json"
        __test_record_pass "seed claude wrote .claude/settings.json"
        set -l content (cat "$tmp/.claude/settings.json")
        assert_contains "seeded claude file has bundled sandbox marker" "$content" \
            '"sandbox"'
        assert_contains "seeded claude file enables sandbox by default" "$content" \
            '"enabled": true'
    else
        __test_record_fail "seed claude wrote .claude/settings.json" \
            "file missing at $tmp/.claude/settings.json"
    end
end

function test_seed_opencode_writes_fixture_into_fresh_repo
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        set -x ATB_USER_PWD '$tmp'
        source '$SCRIPT'
        slop-agents seed opencode
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_status "slop-agents seed opencode status" $rc 0
    if test -f "$tmp/opencode.json"
        __test_record_pass "seed opencode wrote opencode.json"
        set -l content (cat "$tmp/opencode.json")
        assert_contains "seeded opencode file declares schema" "$content" \
            'opencode.ai/config.json'
    else
        __test_record_fail "seed opencode wrote opencode.json" \
            "file missing at $tmp/opencode.json"
    end
end

function test_seed_all_writes_both
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        set -x ATB_USER_PWD '$tmp'
        source '$SCRIPT'
        slop-agents seed all
    "
    command fish -c "$body" >/dev/null 2>&1
    if test -f "$tmp/.claude/settings.json"; and test -f "$tmp/opencode.json"
        __test_record_pass "seed all wrote both files"
    else
        __test_record_fail "seed all wrote both files" \
            "missing one of: .claude/settings.json, opencode.json under $tmp"
    end
end

function test_seed_does_not_clobber_existing_settings
    # Pre-existing .claude/settings.json must be left untouched.
    # Regression guard for the principle that seed is opt-in *and*
    # safe — running it twice should never wipe a user-edited file.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/.claude"
    set -l sentinel '{"_test_marker": "user-edited"}'
    echo "$sentinel" > "$tmp/.claude/settings.json"
    set -l body "
        cd '$tmp'
        command git init -q
        set -x ATB_USER_PWD '$tmp'
        source '$SCRIPT'
        slop-agents seed claude
    "
    set -l out (command fish -c "$body" 2>&1)
    assert_contains "seed reports already-present" "$out" "already present"
    set -l after (cat "$tmp/.claude/settings.json")
    assert_eq "seed did not clobber existing settings" "$after" "$sentinel"
end

function test_seed_outside_git_repo_fails_clearly
    # Without a git toplevel, repo-root resolution fails. The error must
    # be actionable — name the fixture path so the user can copy it
    # manually.
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        source '$SCRIPT'
        slop-agents seed claude
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "seed outside repo fails" $rc 1
    assert_contains "seed outside repo mentions repo root" "$out" "repo root"
    assert_contains "seed outside repo names fixture path" "$out" \
        "claude-code.settings.json"
end

function test_seed_unknown_target_fails
    set -l out (__invoke seed bogus)
    set -l rc $status
    assert_eq "seed unknown target fails" $rc 1
    assert_contains "seed unknown target message" "$out" "unknown seed target"
end

function test_seed_no_target_fails
    set -l out (__invoke seed)
    set -l rc $status
    assert_eq "seed no target fails" $rc 1
    assert_contains "seed no target mentions usage" "$out" "claude|opencode|all"
end

function test_resolve_root_prefers_cwd_over_repo_root
    # cwd has its own .claude/settings.json → resolve picks cwd. Repo
    # root also has one → resolve must NOT pick the root.
    set -l tmp (mk_tmpdir)
    set -l sub "$tmp/sub"
    mkdir -p "$sub/.claude" "$tmp/.claude"
    echo '{"loc":"sub"}'  > "$sub/.claude/settings.json"
    echo '{"loc":"root"}' > "$tmp/.claude/settings.json"
    set -l body "
        cd '$sub'
        command git init -q '$tmp' >/dev/null
        set -x ATB_USER_PWD '$sub'
        source '$SCRIPT'
        __slop_agents_resolve_root claude
    "
    set -l out (command fish -c "$body" 2>&1)
    assert_eq "resolve_root picks cwd when both have settings" \
        (string trim -- "$out") "$sub"
end

function test_resolve_root_falls_back_to_repo_root
    # cwd has no .claude/, repo root does → resolve picks the root.
    # Canonicalize via `path resolve` because macOS's /tmp is a symlink
    # to /private/tmp, and `git rev-parse --show-toplevel` resolves it,
    # so a string-equality compare against the unresolved tmp path
    # fails on Darwin.
    set -l tmp (mk_tmpdir)
    set -l sub "$tmp/sub"
    mkdir -p "$sub" "$tmp/.claude"
    echo '{"loc":"root"}' > "$tmp/.claude/settings.json"
    set -l body "
        command git init -q '$tmp' >/dev/null
        cd '$sub'
        set -x ATB_USER_PWD '$sub'
        source '$SCRIPT'
        __slop_agents_resolve_root claude
    "
    set -l out (string trim -- (command fish -c "$body" 2>&1))
    set -l expected (path resolve "$tmp")
    set -l actual (path resolve "$out")
    assert_eq "resolve_root falls back to repo root" "$actual" "$expected"
end

function test_resolve_root_empty_when_no_overrides
    # Neither cwd nor repo root has a settings file → resolve_root
    # echoes nothing (caller decides what to do).
    set -l tmp (mk_tmpdir)
    set -l body "
        command git init -q '$tmp' >/dev/null
        cd '$tmp'
        set -x ATB_USER_PWD '$tmp'
        source '$SCRIPT'
        set -l result (__slop_agents_resolve_root claude)
        echo \"len=\"(string length -- \"\$result\")
    "
    set -l out (command fish -c "$body" 2>&1)
    assert_contains "resolve_root empty when no override exists" "$out" "len=0"
end

function test_install_advertises_module_name
    # The conf.d snippet generated by slop-install must include
    # slop-agents in its module list, otherwise installed shells will not
    # have the function available.
    set -l content (cat "$REPO_ROOT/scripts/slop-install.fish")
    assert_contains "slop-install knows about slop-agents" "$content" "slop-agents"
end

run_tests_in_file (basename (status filename))
