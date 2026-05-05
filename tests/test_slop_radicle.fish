#!/usr/bin/env fish

# Tests for scripts/slop-radicle.fish — sourced module.
# We do not generate real keys; we exercise help and arg-validation paths.

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-radicle.fish"

function __invoke
    command fish -c "source '$SCRIPT'; slop-radicle $argv" 2>&1
end

function test_no_args_prints_usage_and_fails
    set -l out (__invoke)
    set -l rc $status
    assert_eq "slop-radicle no-args fails" $rc 1
    assert_contains "slop-radicle no-args mentions Usage" "$out" "Usage:"
end

function test_help_flag
    set -l out (__invoke --help)
    set -l rc $status
    assert_status "slop-radicle --help status" $rc 0
    assert_contains "slop-radicle --help mentions Usage" "$out" "Usage:"
    assert_contains "slop-radicle --help mentions create-identity" "$out" "create-identity"
    assert_contains "slop-radicle --help mentions bind-repo" "$out" "bind-repo"
end

function test_unknown_argument_fails
    set -l out (__invoke list-identities --bogus)
    set -l rc $status
    assert_eq "slop-radicle unknown arg fails" $rc 1
    assert_contains "slop-radicle unknown arg message" "$out" "Unknown argument"
end

function test_unknown_command_fails
    set -l out (__invoke do-not-exist)
    set -l rc $status
    assert_eq "slop-radicle unknown cmd fails" $rc 1
    assert_contains "slop-radicle unknown cmd message" "$out" "Unknown command"
end

function test_invalid_rid_rejected
    # bind-repo with an obviously bad RID should be rejected. require_tools may
    # fire first depending on env, so we only require non-zero exit.
    set -l out (__invoke bind-repo --rid not-a-rad --identity-id rid-x --access ro)
    set -l rc $status
    assert_eq "slop-radicle invalid rid fails" $rc 1
end

function test_invalid_access_rejected
    set -l out (__invoke bind-repo --rid rad:z3abcDEF --identity-id rid-x --access bogus)
    set -l rc $status
    assert_eq "slop-radicle invalid access fails" $rc 1
end

function test_print_env_requires_id
    set -l out (__invoke print-env)
    set -l rc $status
    assert_eq "slop-radicle print-env no id fails" $rc 1
    assert_contains "slop-radicle print-env message" "$out" "--identity-id"
end

function test_help_advertises_here_and_tui
    set -l out (__invoke --help)
    assert_contains "slop-radicle help mentions here" "$out" "here info"
    assert_contains "slop-radicle help mentions tui" "$out" "slop-radicle tui"
end

function test_here_requires_subcommand
    set -l out (__invoke here)
    set -l rc $status
    assert_eq "slop-radicle here no-sub fails" $rc 1
    assert_contains "slop-radicle here no-sub message" "$out" "requires a subcommand"
end

function test_here_outside_radicle_repo_fails
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        source '$SCRIPT'
        slop-radicle here info
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-radicle here outside-radicle fails" $rc 1
    assert_contains "slop-radicle here outside-radicle message" "$out" "could not infer Radicle RID"
end

function test_here_info_returns_inferred_rid
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        command git config --local rad.id 'rad:z3test123'
        source '$SCRIPT'
        slop-radicle here info
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_status "slop-radicle here info status" $rc 0
    assert_contains "slop-radicle here info prints rid" "$out" "rad:z3test123"
end

function test_tui_without_gum_prints_install_hint
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    set -l body "
        set -x PATH '$tmp/bin'
        source '$SCRIPT'
        slop-radicle tui
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-radicle tui no-gum fails" $rc 1
    assert_contains "slop-radicle tui no-gum mentions gum" "$out" "gum"
    assert_contains "slop-radicle tui no-gum suggests brew install" "$out" "brew install gum"
end

# ---------------------------------------------------------------------------
# Repo-uniqueness checks. Radicle's model differs from gh-key/forgejo —
# identities are session-scoped, not repo-scoped. Each repo (RID) is then
# *bound* to one or more identities with a per-binding access level.
# So "repo-uniqueness" here means: bindings for RID-A are isolated from
# bindings for RID-B in the state file, and unbinding RID-A leaves RID-B
# untouched. See plans/are-the-keys-we-quiet-music.md for the broader
# context across all three forges.
# ---------------------------------------------------------------------------

function test_generate_identity_key_filenames_distinct_across_calls
    # Two consecutive __llm_rad_generate_identity_key calls produce
    # distinct on-disk paths and distinct ed25519 public-key bytes —
    # even within the same UTC second, since the identity id embeds a
    # UUID8 from llm_radicle_access.py.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/keys"
    set -l body "
        source '$SCRIPT'
        set LLM_RADICLE_KEY_DIR '$tmp/keys'
        __llm_rad_generate_identity_key sess-a 2026-05-06T00:00:00Z
        or exit 1
        echo \"id_a=\$__llm_rad_last_identity_id\"
        echo \"path_a=\$__llm_rad_last_identity_key\"
        __llm_rad_generate_identity_key sess-b 2026-05-06T00:00:00Z
        or exit 1
        echo \"id_b=\$__llm_rad_last_identity_id\"
        echo \"path_b=\$__llm_rad_last_identity_key\"
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l id_a (string match -r 'id_a=(.+)' -- $out)[2]
    set -l path_a (string match -r 'path_a=(.+)' -- $out)[2]
    set -l id_b (string match -r 'id_b=(.+)' -- $out)[2]
    set -l path_b (string match -r 'path_b=(.+)' -- $out)[2]
    if test -z "$id_a" -o -z "$id_b"
        __test_record_fail "radicle generate produced both identity ids" "out=$out"
        return
    end
    if test "$id_a" != "$id_b"
        __test_record_pass "radicle two calls → distinct identity ids"
    else
        __test_record_fail "radicle two calls → distinct identity ids" \
            "both calls returned $id_a — UUID8 generator may be deterministic"
    end
    if test "$path_a" != "$path_b"
        __test_record_pass "radicle two calls → distinct on-disk filenames"
    else
        __test_record_fail "radicle two calls → distinct on-disk filenames" \
            "both calls wrote $path_a"
    end
    # Public-key contents must differ — guard against a future regression
    # where someone introduces a deterministic seed and breaks isolation.
    if test -f "$path_a.pub" -a -f "$path_b.pub"
        set -l pub_a (cat "$path_a.pub")
        set -l pub_b (cat "$path_b.pub")
        if test "$pub_a" != "$pub_b"
            __test_record_pass "radicle two calls → distinct ed25519 public keys"
        else
            __test_record_fail "radicle two calls → distinct ed25519 public keys" \
                "ssh-keygen produced the same public key for both calls"
        end
    end
end

function test_bindings_are_isolated_across_rids
    # Radicle's "repo-uniqueness" lives in the state file's bindings list:
    # binding rid-A to identity X must not appear under rid-B. Conversely
    # binding rid-B to identity X must coexist independently with rid-A's
    # binding. The Python helper indexes bindings by (rid, identity_id),
    # so this is well-defined; this test pins the behaviour down end-to-end
    # through the fish wrappers, which is what real callers use.
    set -l tmp (mk_tmpdir)
    set -l state "$tmp/radicle-access.json"
    set -l body "
        source '$SCRIPT'
        set -g LLM_RADICLE_CONFIG_DIR '$tmp'
        set -g LLM_RADICLE_STATE_FILE '$state'
        # Seed two active identities so bind_repo's existence-check passes.
        echo '{\"identities\":[{\"id\":\"ident-X\",\"name\":\"x\",\"status\":\"active\"},{\"id\":\"ident-Y\",\"name\":\"y\",\"status\":\"active\"}],\"bindings\":[]}' > '$state'
        __llm_rad_bind_repo rad:z3aaa ident-X ro session-a >/dev/null
        or exit 1
        __llm_rad_bind_repo rad:z3bbb ident-X rw session-b >/dev/null
        or exit 1
    "
    command fish -c "$body" >/dev/null 2>&1
    if not test -f "$state"
        __test_record_fail "radicle state file written" "no state file"
        return
    end
    # rid-A's listing only sees its own binding; rid-B only sees its own.
    set -l only_a (command fish -c "
        source '$SCRIPT'
        set -g LLM_RADICLE_STATE_FILE '$state'
        __llm_rad_list_bindings rad:z3aaa false
    " 2>&1)
    assert_contains "RID-A binding lists ident-X ro" "$only_a" "ident-X"
    assert_contains "RID-A binding has ro access" "$only_a" "ro"
    assert_not_contains "RID-A listing leaks rad:z3bbb rows" "$only_a" "z3bbb"

    set -l only_b (command fish -c "
        source '$SCRIPT'
        set -g LLM_RADICLE_STATE_FILE '$state'
        __llm_rad_list_bindings rad:z3bbb false
    " 2>&1)
    assert_contains "RID-B binding lists ident-X rw" "$only_b" "ident-X"
    assert_not_contains "RID-B listing leaks rad:z3aaa rows" "$only_b" "z3aaa"

    # Now unbind RID-A. RID-B's binding must still be active.
    command fish -c "
        source '$SCRIPT'
        set -g LLM_RADICLE_STATE_FILE '$state'
        __llm_rad_unbind_repo rad:z3aaa ''
    " >/dev/null 2>&1
    set -l after_unbind (command fish -c "
        source '$SCRIPT'
        set -g LLM_RADICLE_STATE_FILE '$state'
        __llm_rad_list_bindings rad:z3bbb false
    " 2>&1)
    assert_contains "RID-B survives RID-A unbind" "$after_unbind" "ident-X"
end

run_tests_in_file (basename (status filename))
