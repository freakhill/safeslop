#!/usr/bin/env fish

# Tests for scripts/slop-isolate.fish + scripts/_py/isolation.py.
#
# Help paths run on every OS. CUE-dependent paths require `cue` on PATH;
# tests skip cleanly when it's missing so contributors without cue still
# see the rest of the suite pass.

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-isolate.fish"
set -g ISOLATION_PY "$SCRIPTS_DIR/_py/isolation.py"
set -g PRESET_USER_CONFIG "$REPO_ROOT/library/isolation/examples/user-config.cue"

function __invoke
    command fish -c "source '$SCRIPT'; slop-isolate $argv" 2>&1
end

function __have_cue
    command -sq cue; and command -sq uv
end

# ---------------------------------------------------------------------------
# Help paths.
# ---------------------------------------------------------------------------

function test_help_subcommand
    set -l out (__invoke help)
    set -l rc $status
    assert_status "slop-isolate help status" $rc 0
    assert_contains "slop-isolate help mentions Usage" "$out" "Usage:"
    assert_contains "slop-isolate help mentions presets" "$out" "presets"
    assert_contains "slop-isolate help mentions adapters" "$out" "Adapters:"
end

function test_dash_dash_help
    set -l out (__invoke --help)
    set -l rc $status
    assert_status "slop-isolate --help status" $rc 0
    assert_contains "slop-isolate --help mentions Usage" "$out" "Usage:"
end

function test_no_args_prints_help
    set -l out (__invoke)
    set -l rc $status
    assert_status "slop-isolate no-args status" $rc 0
    assert_contains "slop-isolate no-args mentions Usage" "$out" "Usage:"
end

function test_unknown_subcommand_errors
    set -l out (__invoke nonexistent-thing)
    set -l rc $status
    assert_eq "slop-isolate unknown subcommand fails" $rc 1
    assert_contains "slop-isolate unknown subcommand prints Error" "$out" "Error: Unknown command"
end

# ---------------------------------------------------------------------------
# Preset listing (requires cue + uv).
# ---------------------------------------------------------------------------

function test_presets_list_contains_all_ten
    if not __have_cue
        __test_record_pass "slop-isolate presets list (skipped: cue/uv missing)"
        return 0
    end
    set -l out (__invoke presets list)
    set -l rc $status
    assert_status "slop-isolate presets list status" $rc 0
    for p in any-agent claude-code opencode crewai pydantic-ai ag2 openclaw zeroclaw nous-hermes-local nous-hermes-remote
        assert_contains "presets list contains $p" "$out" "$p"
    end
end

function test_presets_show_unknown_errors
    if not __have_cue
        __test_record_pass "slop-isolate presets show unknown (skipped)"
        return 0
    end
    set -l out (__invoke presets show definitely-not-a-preset)
    set -l rc $status
    assert_eq "slop-isolate presets show unknown fails" $rc 2
    assert_contains "slop-isolate presets show unknown prints Error" "$out" "unknown preset"
end

function test_presets_show_emits_json
    if not __have_cue
        __test_record_pass "slop-isolate presets show json (skipped)"
        return 0
    end
    set -l out (__invoke presets show claude-code)
    set -l rc $status
    assert_status "slop-isolate presets show status" $rc 0
    assert_contains "slop-isolate presets show emits json name" "$out" '"name": "claude-code"'
    assert_contains "slop-isolate presets show domains include anthropic" "$out" "api.anthropic.com"
end

# ---------------------------------------------------------------------------
# Validate / compile path with the demo user config.
# ---------------------------------------------------------------------------

function test_validate_user_config
    if not __have_cue
        __test_record_pass "slop-isolate validate user-config (skipped)"
        return 0
    end
    set -l out (__invoke validate "$PRESET_USER_CONFIG")
    set -l rc $status
    assert_status "slop-isolate validate user-config status" $rc 0
    assert_contains "slop-isolate validate confirms" "$out" "validates against the schema"
end

function test_compile_claude_code_settings_emits_extra_domain
    if not __have_cue
        __test_record_pass "slop-isolate compile claude-code-settings (skipped)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l out (__invoke compile "$PRESET_USER_CONFIG" --adapter claude-code-settings --out "$tmp")
    set -l rc $status
    assert_status "slop-isolate compile status" $rc 0
    set -l settings_path "$tmp/claude-code.settings.json"
    if not test -f "$settings_path"
        __test_record_fail "slop-isolate compile produced settings.json" "missing $settings_path"
        return 1
    end
    set -l body (cat "$settings_path")
    assert_contains "settings.json contains preset domain" "$body" "api.anthropic.com"
    assert_contains "settings.json contains user-extra domain" "$body" "github.example.internal"
    assert_contains "settings.json denies ssh reads" "$body" "~/.ssh/**"
end

function test_compile_sandbox_exec_records_lossy_note
    if not __have_cue
        __test_record_pass "slop-isolate compile sandbox-exec (skipped)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l out (__invoke compile "$PRESET_USER_CONFIG" --adapter sandbox-exec --out "$tmp")
    set -l rc $status
    assert_status "slop-isolate compile sandbox-exec status" $rc 0
    set -l profile (cat "$tmp/claude-code.sb")
    assert_contains "sandbox-exec profile is version 1" "$profile" "(version 1)"
    assert_contains "sandbox-exec profile denies default" "$profile" "(deny default)"
    assert_contains "sandbox-exec profile records lossy domain note" "$profile" "not enforced by sandbox-exec"
end

function test_compile_pf_strict_fails_when_fallback_fail
    if not __have_cue
        __test_record_pass "slop-isolate compile pf strict (skipped)"
        return 0
    end
    # The user config sets tool.pf.domain-fallback = "fail"; pf must refuse.
    set -l tmp (mk_tmpdir)
    set -l out (__invoke compile "$PRESET_USER_CONFIG" --adapter pf --out "$tmp")
    set -l rc $status
    assert_eq "slop-isolate pf with domain-fallback=fail returns 3" $rc 3
    assert_contains "pf failure message mentions fallback" "$out" "domain-fallback=fail"
end

run_tests_in_file
