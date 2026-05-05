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

function test_wrapper_sets_uv_native_tls
    # uv's bundled rustls fails on machines behind a TLS-intercepting proxy
    # (Cloudflare/Zscaler/corporate MITM) when fetching Textual on first run.
    # The wrapper must opt into the OS trust store via UV_NATIVE_TLS so the
    # first-run install does not bounce on UnknownIssuer.
    set -l content (cat "$SCRIPT")
    assert_contains "slop.fish enables UV_NATIVE_TLS" "$content" "UV_NATIVE_TLS"
end

function test_wrapper_has_layered_tls_fallback
    # The wrapper must try at least three TLS strategies before giving up:
    # OS trust store (UV_NATIVE_TLS), explicit cert bundle (SSL_CERT_FILE),
    # and an opt-in --allow-insecure-host bypass for users behind an
    # un-trustable proxy. Without the layered fallback, `slop --check`
    # cannot diagnose where the chain breaks.
    set -l content (cat "$SCRIPT")
    assert_contains "wrapper sets SSL_CERT_FILE fallback" "$content" "SSL_CERT_FILE"
    assert_contains "wrapper documents --allow-insecure-host" "$content" "allow-insecure-host"
    assert_contains "wrapper gates insecure-host on SLOP_INSECURE_HOSTS" "$content" "SLOP_INSECURE_HOSTS"
end

function test_check_subcommand_exists
    # `slop --check` must be on the no-uv-needed fast path of the dispatcher,
    # so a user behind a broken-TLS environment can still get an actionable
    # error rather than a stack trace.
    set -l content (cat "$SCRIPT")
    assert_contains "slop.fish dispatches --check" "$content" "case --check"
end

function test_self_check_actually_resolves_textual
    # End-to-end: invoke `slop --check` and confirm uv can fetch Textual.
    # This is the canonical "did the TLS workaround land?" test for users
    # hitting UnknownIssuer. Skip when uv is missing or PyPI is unreachable
    # so the suite still runs offline / without uv.
    if not command -sq uv
        __test_record_pass "slop --check (skipped: uv missing)"
        return 0
    end
    if not curl -fsSL --max-time 5 -o /dev/null https://pypi.org/simple/ 2>/dev/null
        __test_record_pass "slop --check (skipped: pypi.org unreachable)"
        return 0
    end
    set -l out (run_fish $SCRIPT --check 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "slop --check resolves textual" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "slop --check resolves textual"
    assert_contains "slop --check confirms an OK strategy" "$out" "OK"
end

function test_self_check_entry_point_in_python_module
    # The fish wrapper passes --self-check to the Python module to short-
    # circuit the App.run() and just confirm the import succeeded. The
    # entry point must exist or `slop --check` will start an interactive
    # TUI from inside a non-TTY test runner and hang.
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "slop_tui.py honors --self-check" "$content" "--self-check"
end

function test_check_strategies_appear_in_documented_order
    # The four TLS strategies must be tried in order of decreasing safety:
    # rustls defaults → OS trust store → system cert bundle → opt-in
    # insecure-host bypass. If the order regresses, users behind a proxy
    # would either get false-OK on insecure paths first, or never reach
    # the strategy that would have worked on a clean network. Anchor on
    # the `# Strategy N:` markers because they appear exactly once per
    # strategy and only inside the function body (not in the header doc).
    set -l ln1 (grep -n '# Strategy 1:' "$SCRIPT" | head -n1 | string split -m1 ':')[1]
    set -l ln2 (grep -n '# Strategy 2:' "$SCRIPT" | head -n1 | string split -m1 ':')[1]
    set -l ln3 (grep -n '# Strategy 3:' "$SCRIPT" | head -n1 | string split -m1 ':')[1]
    set -l ln4 (grep -n '# Strategy 4:' "$SCRIPT" | head -n1 | string split -m1 ':')[1]
    if test -z "$ln1" -o -z "$ln2" -o -z "$ln3" -o -z "$ln4"
        __test_record_fail "TLS strategy markers present" "lines: 1=$ln1 2=$ln2 3=$ln3 4=$ln4"
        return
    end
    __test_record_pass "TLS strategy markers present"
    if test "$ln1" -lt "$ln2"; and test "$ln2" -lt "$ln3"; and test "$ln3" -lt "$ln4"
        __test_record_pass "TLS strategies appear in documented order"
    else
        __test_record_fail "TLS strategies appear in documented order" \
            "lines: 1=$ln1 2=$ln2 3=$ln3 4=$ln4"
    end
end

function test_interactive_launch_env_resolves_textual
    # End-to-end test for the production launch path. `slop --check` only
    # exercises strategy 1 (no env vars) when the network is healthy, so it
    # does not tell us whether the env vars `__slop_exec_tui` sets — the
    # ones that fire on every real `slop` invocation — actually resolve
    # Textual. This test mirrors that env exactly and asserts that uv can
    # fetch the dependency. If this fails, users on this machine will hit
    # the original UnknownIssuer error every time they type `slop`.
    if not command -sq uv
        __test_record_pass "interactive launch env (skipped: uv missing)"
        return 0
    end
    if not curl -fsSL --max-time 5 -o /dev/null https://pypi.org/simple/ 2>/dev/null
        __test_record_pass "interactive launch env (skipped: pypi.org unreachable)"
        return 0
    end
    set -l envs UV_NATIVE_TLS=1
    if test -f /etc/ssl/cert.pem
        set -a envs SSL_CERT_FILE=/etc/ssl/cert.pem
    end
    set -l out (env $envs uv run --script --quiet "$SLOP_TUI_PY" --self-check 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "interactive launch env resolves textual" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "interactive launch env resolves textual"
    assert_contains "self-check confirms textual import" "$out" "textual import OK"
end

function test_spawn_with_ctty_propagates_exit_codes
    # Regression: the previous run_subprocess used subprocess.call, which
    # left the child in the parent's process group. Interactive children
    # like `slop-macos-sandbox shell` (zsh) hit
    #   "zsh: can't set tty pgrp: operation not permitted"
    # and dropped back to the menu before the user could type anything.
    # The fix is _spawn_with_ctty: fork → setpgid the child into its own
    # group → tcsetpgrp it to foreground → exec. We can't exercise the
    # interactive zsh path from CI (no real ctty under fish-c), but we
    # can pin the helper's exit-code propagation for non-interactive
    # children — which is the more common case anyway and would break
    # silently if a future refactor reverted to subprocess.call without
    # the WIFEXITED/WTERMSIG handling.
    if not command -sq uv
        __test_record_pass "_spawn_with_ctty (skipped: uv missing)"
        return 0
    end
    if not curl -fsSL --max-time 5 -o /dev/null https://pypi.org/simple/ 2>/dev/null
        __test_record_pass "_spawn_with_ctty (skipped: pypi.org unreachable)"
        return 0
    end
    set -l envs UV_NATIVE_TLS=1
    if test -f /etc/ssl/cert.pem
        set -a envs SSL_CERT_FILE=/etc/ssl/cert.pem
    end
    set -l py "
import sys
sys.path.insert(0, 'scripts/_py')
from slop_tui import _spawn_with_ctty
assert _spawn_with_ctty(['true']) == 0, 'true should exit 0'
assert _spawn_with_ctty(['false']) == 1, 'false should exit 1'
assert _spawn_with_ctty(['sh', '-c', 'exit 42']) == 42, 'exit-42 should propagate'
print('OK')
"
    set -l out (env $envs uv run --quiet --with 'textual>=0.79' python -c "$py" 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "_spawn_with_ctty propagates exit codes" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "_spawn_with_ctty propagates exit codes"
    assert_contains "_spawn_with_ctty asserts all passed" "$out" "OK"
end

function test_run_subprocess_uses_spawn_with_ctty_not_subprocess_call
    # Static guard: run_subprocess in scripts/_py/slop_tui.py must invoke
    # _spawn_with_ctty, not subprocess.call. If a future refactor swaps
    # back to subprocess.call, the interactive shell action breaks again
    # — and CI without a real ctty would not catch it dynamically.
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "run_subprocess delegates to _spawn_with_ctty" \
        "$content" "_spawn_with_ctty(argv)"
    # The previous bug shape: a bare `subprocess.call(argv)` inside
    # run_subprocess. Such a line should no longer exist.
    set -l hits (grep -c 'rc = subprocess.call(argv)' "$SLOP_TUI_PY")
    assert_eq "no bare subprocess.call(argv) on the run_subprocess path" "$hits" "0"
end

function test_self_check_marker_proves_textual_imported
    # The Python module imports textual at module top, so reaching the
    # `--self-check` print statement proves the dependency was both fetched
    # by uv and importable by Python. A weaker test (just asserting "OK"
    # somewhere in the wrapper's output) would pass even if the wrapper
    # printed "OK" without ever exercising textual. This test is the
    # canonical anchor for the bug report — if this passes, the original
    # UnknownIssuer no longer reproduces in this environment.
    if not command -sq uv
        __test_record_pass "self-check textual import (skipped: uv missing)"
        return 0
    end
    if not curl -fsSL --max-time 5 -o /dev/null https://pypi.org/simple/ 2>/dev/null
        __test_record_pass "self-check textual import (skipped: pypi.org unreachable)"
        return 0
    end
    set -l out (run_fish $SCRIPT --check 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "self-check textual import" "rc=$rc, out=$out"
        return
    end
    assert_contains "self-check ran the python entry point" "$out" "textual import OK"
end

function test_app_mounts_without_errors
    # Regression: a previous build crashed at launch with `NoMatches: No
    # nodes match '#list' on MenuScreen()` because the filter_text
    # reactive's watcher fired before compose() created the #list widget.
    # Fixed by declaring the reactive with init=False. This test drives
    # the App through Textual's headless run_test() driver so an analogous
    # regression cannot ship again — a `--self-check` pass alone is not
    # enough, since import success says nothing about mount success.
    if not command -sq uv
        __test_record_pass "app mount-check (skipped: uv missing)"
        return 0
    end
    if not curl -fsSL --max-time 5 -o /dev/null https://pypi.org/simple/ 2>/dev/null
        __test_record_pass "app mount-check (skipped: pypi.org unreachable)"
        return 0
    end
    set -l envs UV_NATIVE_TLS=1
    if test -f /etc/ssl/cert.pem
        set -a envs SSL_CERT_FILE=/etc/ssl/cert.pem
    end
    set -l out (env $envs uv run --script --quiet "$SLOP_TUI_PY" --mount-check 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "app mounts without errors" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "app mounts without errors"
    assert_contains "mount-check confirms App.run_test() completed" "$out" "mount-check OK"
end

function test_skills_install_action_runs_with_dry_run
    # End-to-end: invoke the same argv that the Skills > "Preview --dry-run"
    # action fires from the TUI, and assert the script accepts it (no
    # "Unknown argument" / "unknown command" errors). Catches the bug
    # where the action passed `install` as a positional verb to a script
    # that only accepts flags.
    set -l out (run_fish "$REPO_ROOT/scripts/slop-skills-install.fish" --dry-run 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "slop-skills-install --dry-run runs cleanly" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "slop-skills-install --dry-run runs cleanly"
    assert_not_contains "no Unknown argument error" "$out" "Unknown argument"
    assert_not_contains "no unknown command error" "$out" "unknown command"
end

function test_action_tree_audit_passes
    # Walks the entire action tree under build_top_actions() and verifies
    # no leaf still shells out to a `slop-X tui` flow, every leaf has a
    # runnable argv, and prompt placeholders fully resolve. This is the
    # canonical regression net for "the whole thing must be Python +
    # Textual" — if any future edit reintroduces a fish/gum tui call from
    # the launcher, this fails with a specific FAIL line per offender.
    if not command -sq uv
        __test_record_pass "action-tree audit (skipped: uv missing)"
        return 0
    end
    if not curl -fsSL --max-time 5 -o /dev/null https://pypi.org/simple/ 2>/dev/null
        __test_record_pass "action-tree audit (skipped: pypi.org unreachable)"
        return 0
    end
    set -l envs UV_NATIVE_TLS=1
    if test -f /etc/ssl/cert.pem
        set -a envs SSL_CERT_FILE=/etc/ssl/cert.pem
    end
    set -l out (env $envs uv run --script --quiet "$SLOP_TUI_PY" --audit 2>&1)
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "action-tree audit passes" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "action-tree audit passes"
    assert_contains "audit reports a leaf-action count" "$out" "leaf actions"
end

function test_python_tui_does_not_shell_into_legacy_fish_tui
    # Belt-and-suspenders static check (no network/uv needed): the Python
    # source must not call any per-tool fish script with `tui` as its
    # subcommand. Catches a regression even when the audit test is skipped
    # offline.
    set -l content (cat "$SLOP_TUI_PY")
    assert_not_contains "no slop-gh-key tui shell-out" "$content" 'slop-gh-key tui'
    assert_not_contains "no slop-forgejo-key tui shell-out" "$content" 'slop-forgejo-key tui'
    assert_not_contains "no slop-radicle tui shell-out" "$content" 'slop-radicle tui'
    assert_not_contains "no slop-agent-sandbox tui shell-out" "$content" 'slop-agent-sandbox tui'
    assert_not_contains "no slop-brew-vm tui shell-out" "$content" 'slop-brew-vm tui'
end

function test_python_tui_defines_modal_screens
    # Replacing gum confirm/input requires native Textual modals. If these
    # disappear, action firing falls back to silent run-without-prompts —
    # which would skip destructive-action confirms (e.g. revoke-all).
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "InputScreen modal exists" "$content" "class InputScreen"
    assert_contains "ConfirmScreen modal exists" "$content" "class ConfirmScreen"
    assert_contains "InputScreen subclasses ModalScreen" "$content" "InputScreen(ModalScreen"
    assert_contains "ConfirmScreen subclasses ModalScreen" "$content" "ConfirmScreen(ModalScreen"
end

function test_action_inputs_are_shell_quoted
    # _fish_invocation must shlex.quote each interpolated arg so user-
    # supplied inputs (e.g. a brew-vm `run` command containing single
    # quotes) cannot break out of the fish -c command string. Without
    # shell-quoting, an input like  ; rm -rf /  would be catastrophic.
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "_fish_invocation shell-quotes args" "$content" "shlex.quote"
end

function test_filter_reactive_skips_initial_fire
    # Static guard: if someone removes init=False from the filter_text
    # reactive, the watcher will fire before compose() — the exact bug
    # the mount-check catches at runtime. Belt-and-suspenders so the
    # regression is caught even when the network/uv-cache test is skipped.
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "filter_text reactive skips initial watcher fire" \
        "$content" 'reactive("", init=False)'
end

function test_python_module_imports_textual_at_load_time
    # If textual were lazily imported inside main(), `--self-check` could
    # print OK without ever exercising the dependency — masking a broken
    # PyPI fetch. Confirm the import happens at module top so reaching
    # main() at all proves textual resolved.
    set -l content (cat "$SLOP_TUI_PY")
    assert_contains "slop_tui.py imports textual at module level" "$content" "from textual.app import App"
end

run_tests_in_file (basename (status filename))
