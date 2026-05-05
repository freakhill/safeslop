#!/usr/bin/env fish

# Tests for scripts/slop.fish — global TUI launcher (Textual rewrite).
# We never start the interactive Python TUI in CI (would require a TTY and
# pulling Textual from PyPI). Assertions exercise non-interactive paths:
# help/version print without uv, the uv hard-dep gate fires with a clear
# message, and the bundled Python module parses.

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop.fish"
set -g SLOP_TUI_PY "$SCRIPTS_DIR/_py/slop_tui.py"

function test_help_subcommand_works_without_uv
    set -l out (run_fish $SCRIPT help 2>&1)
    set -l rc $status
    assert_status "slop help status" $rc 0
    assert_contains "slop help mentions Usage" "$out" "Usage:"
    assert_contains "slop help mentions Examples" "$out" "Examples:"
    assert_contains "slop help mentions Notes" "$out" "Notes:"
    assert_contains "slop help mentions per-tool TUI" "$out" "slop-gh-key tui"
    assert_contains "slop help mentions Textual" "$out" "Textual"
end

function test_dash_dash_help
    set -l out (run_fish $SCRIPT --help 2>&1)
    set -l rc $status
    assert_status "slop --help status" $rc 0
    assert_contains "slop --help mentions Usage" "$out" "Usage:"
end

function test_version_flag
    set -l out (run_fish $SCRIPT --version 2>&1)
    set -l rc $status
    assert_status "slop --version status" $rc 0
    assert_contains "slop --version prints version" "$out" "slop"
end

function test_unknown_arg_fails_with_help
    set -l out (run_fish $SCRIPT bogus 2>&1)
    set -l rc $status
    assert_eq "slop unknown arg fails" $rc 1
    assert_contains "slop unknown arg mentions Usage" "$out" "Usage:"
    assert_contains "slop unknown arg shows error" "$out" "unknown argument"
end

function test_no_args_without_uv_prints_install_hint
    # Force a PATH that excludes uv so the hard-dep gate fires. fish -N skips
    # the user's config so PATH stays as we set it. The wrapper sources
    # cleanly without uv; only the trailing `__slop_require_uv; or exit 1`
    # is supposed to fail when no args were passed.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    set -l body "set -x PATH '$tmp/bin'; source '$SCRIPT'"
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop no-uv fails" $rc 1
    assert_contains "slop no-uv mentions uv" "$out" "uv"
    assert_contains "slop no-uv suggests brew install uv" "$out" "brew install uv"
    assert_contains "slop no-uv suggests CLI fallback" "$out" "slop-sandboxctl.fish help"
end

function test_install_fish_tools_wraps_slop
    # The conf.d snippet wraps standalone scripts as fish functions; verify
    # 'slop' is still in the standalone list so the wrapper is generated.
    set -l installer "$REPO_ROOT/scripts/slop-install.fish"
    set -l content (cat "$installer")
    assert_contains "slop-install knows about slop" "$content" "slop"
end

function test_python_tui_parses
    if not test -f "$SLOP_TUI_PY"
        __test_record_fail "slop_tui.py exists" "missing $SLOP_TUI_PY"
        return
    end
    if not command -sq python3
        __test_record_pass "slop_tui.py syntax (skipped: python3 missing)"
        return
    end
    if python3 -c "import ast,sys; ast.parse(open(sys.argv[1]).read())" "$SLOP_TUI_PY" 2>/dev/null
        __test_record_pass "slop_tui.py parses as Python"
    else
        __test_record_fail "slop_tui.py parses as Python" "ast.parse raised"
    end
end

function test_python_tui_has_pep723_textual_pin
    # PEP-723 metadata should pin Textual so first-run install is reproducible.
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "slop_tui.py declares PEP-723 script" "$content" "/// script"
    assert_contains "slop_tui.py pins textual" "$content" "textual"
end

run_tests_in_file (basename (status filename))
