#!/usr/bin/env fish

# Tests for scripts/slop-forgejo-key.fish — sourced module.
# Forgejo API is not called; we exercise help and arg-validation paths.

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-forgejo-key.fish"

function __invoke
    command fish -c "source '$SCRIPT'; slop-forgejo-key $argv" 2>&1
end

function test_no_args_prints_usage_and_fails
    set -l out (__invoke)
    set -l rc $status
    assert_eq "slop-forgejo-key no-args fails" $rc 1
    assert_contains "slop-forgejo-key no-args mentions Usage" "$out" "Usage:"
end

function test_help_flag
    set -l out (__invoke --help)
    set -l rc $status
    assert_status "slop-forgejo-key --help status" $rc 0
    assert_contains "slop-forgejo-key --help mentions Usage" "$out" "Usage:"
    assert_contains "slop-forgejo-key --help mentions instance-set" "$out" "instance-set"
    assert_contains "slop-forgejo-key --help mentions create-pair" "$out" "create-pair"
end

function test_unknown_argument_fails
    set -l out (__invoke list --bogus)
    set -l rc $status
    assert_eq "slop-forgejo-key unknown arg fails" $rc 1
    assert_contains "slop-forgejo-key unknown arg message" "$out" "Unknown argument"
end

function test_unknown_command_fails
    # Some commands run validation in the dispatch switch.
    set -l out (__invoke do-not-exist --repo a/b)
    set -l rc $status
    assert_eq "slop-forgejo-key unknown cmd fails" $rc 1
end

function test_invalid_repo_format_rejected
    # Use create with bogus repo to hit __llm_forgejo_validate_repo.
    # This requires require_tools to pass first; if curl/python3/ssh-keygen are
    # missing the rc will still be 1 with a clear message, which we accept.
    set -l out (__invoke create --repo bogus-no-slash --access ro)
    set -l rc $status
    assert_eq "slop-forgejo-key invalid repo fails" $rc 1
end

function test_help_advertises_here_and_tui
    set -l out (__invoke --help)
    assert_contains "slop-forgejo-key help mentions here" "$out" "here create-pair"
    assert_contains "slop-forgejo-key help mentions tui" "$out" "slop-forgejo-key tui"
end

function test_here_requires_subcommand
    set -l out (__invoke here)
    set -l rc $status
    assert_eq "slop-forgejo-key here no-sub fails" $rc 1
    assert_contains "slop-forgejo-key here no-sub message" "$out" "requires a subcommand"
end

function test_here_outside_git_repo_fails
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        source '$SCRIPT'
        slop-forgejo-key here list
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-forgejo-key here outside-repo fails" $rc 1
    assert_contains "slop-forgejo-key here outside-repo message" "$out" "could not infer repo"
end

function test_here_no_matching_instance_profile_fails_clearly
    # Set up a tmp repo + a tmp instance profile pointing at a DIFFERENT host
    # so the lookup-by-host branch fires. We override LLM_FORGEJO_INSTANCES_FILE
    # to keep the test hermetic and avoid touching ~/.config.
    set -l tmp (mk_tmpdir)
    set -l profile "$tmp/instances.json"
    echo '{"instances":{"main":{"url":"https://other.example.com","token_env":"FORGEJO_TOKEN"}}}' > "$profile"
    set -l body "
        cd '$tmp'
        command git init -q
        command git remote add origin git@forgejo.example.com:owner/repo.git
        source '$SCRIPT'
        set -g LLM_FORGEJO_INSTANCES_FILE '$profile'
        slop-forgejo-key here list
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-forgejo-key here no-instance fails" $rc 1
    assert_contains "slop-forgejo-key here no-instance message" "$out" "no Forgejo instance profile"
    assert_contains "slop-forgejo-key here suggests instance-set" "$out" "instance-set"
end

function test_here_repo_inference_url_forms
    for url in \
        "git@forgejo.example.com:owner/repo.git" \
        "https://forgejo.example.com/owner/repo.git" \
        "ssh://git@forgejo.example.com/owner/repo.git"
        set -l tmp (mk_tmpdir)
        set -l body "
            cd '$tmp'
            command git init -q
            command git remote add origin '$url'
            source '$SCRIPT'
            __llm_forgejo_repo_from_git
        "
        set -l out (command fish -c "$body" 2>&1)
        # Output is two lines: host then owner/repo. Join for the assertion.
        assert_contains "slop-forgejo-key infer host from $url" "$out" "forgejo.example.com"
        assert_contains "slop-forgejo-key infer repo from $url" "$out" "owner/repo"
    end
end

function test_list_header_uses_real_tabs
    # Same regression as in slop-gh-key: fish echo prints literal `\t`,
    # which mismatches the body's real tabs from the Python helper.
    set -l content (cat "$SCRIPT")
    assert_contains "list header uses printf for real tabs" "$content" \
        "printf 'id\\taccess\\tcreated_at\\ttitle"
    assert_not_contains "list header no longer echoes literal backslash-t" "$content" \
        'echo "id\taccess'
end

function test_string_split_uses_real_tab_delimiter
    # Regression: `string split "\t"` does NOT split real tab characters
    # in fish — it splits on the literal two-character `\t` sequence.
    # The Python helpers (llm_forgejo_keys.py) emit real tabs, so the
    # fish caller has to use unquoted `\t` (which fish interprets as a
    # tab) — `string split \t -- $line`. Without this fix, `parts[1]`
    # contains the entire `url<TAB>token<TAB>env` string and `parts[2]`
    # is empty, breaking instance-profile resolution.
    set -l content (cat "$SCRIPT")
    assert_not_contains "no quoted-backslash-t string-split" "$content" \
        'string split "\t"'
end

function test_tui_without_gum_prints_install_hint
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    set -l body "
        set -x PATH '$tmp/bin'
        source '$SCRIPT'
        slop-forgejo-key tui
    "
    set -l out (command fish -N -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-forgejo-key tui no-gum fails" $rc 1
    assert_contains "slop-forgejo-key tui no-gum mentions gum" "$out" "gum"
    assert_contains "slop-forgejo-key tui no-gum suggests brew install" "$out" "brew install gum"
end

# ---------------------------------------------------------------------------
# Repo-uniqueness checks. Mirrors the corresponding section in
# test_slop_gh_key.fish — see plans/are-the-keys-we-quiet-music.md.
# Forgejo has one extra axis vs. GitHub: a `host_name` argument that
# distinguishes which Forgejo *instance* (codeberg.org, self-hosted, etc.)
# the alias points at. The host_prefix collision still happens within a
# single instance.
# ---------------------------------------------------------------------------

function test_generate_key_filenames_distinct_across_names
    # Two consecutive __llm_forgejo_generate_key calls with different
    # --name values produce distinct on-disk paths and distinct ed25519
    # public-key bytes — even within the same UTC second.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/keys"
    set -l body "
        source '$SCRIPT'
        set LLM_FORGEJO_KEY_DIR '$tmp/keys'
        __llm_forgejo_generate_key ro repo-a 2026-05-06T00:00:00Z
        or exit 1
        echo \"path_a=\$__llm_forgejo_last_key_path\"
        echo \"pub_a=\$__llm_forgejo_last_key_pub\"
        __llm_forgejo_generate_key ro repo-b 2026-05-06T00:00:00Z
        or exit 1
        echo \"path_b=\$__llm_forgejo_last_key_path\"
        echo \"pub_b=\$__llm_forgejo_last_key_pub\"
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l path_a (string match -r 'path_a=(.+)' -- $out)[2]
    set -l pub_a (string match -r 'pub_a=(.+)' -- $out)[2]
    set -l path_b (string match -r 'path_b=(.+)' -- $out)[2]
    set -l pub_b (string match -r 'pub_b=(.+)' -- $out)[2]
    if test -z "$path_a" -o -z "$path_b"
        __test_record_fail "forgejo generate_key produced both paths" "out=$out"
        return
    end
    if test "$path_a" != "$path_b"
        __test_record_pass "forgejo two names → distinct on-disk filenames"
    else
        __test_record_fail "forgejo two names → distinct on-disk filenames" \
            "both calls returned $path_a"
    end
    if test "$pub_a" != "$pub_b"
        __test_record_pass "forgejo two names → distinct ed25519 public keys"
    else
        __test_record_fail "forgejo two names → distinct ed25519 public keys" \
            "ssh-keygen produced the same public key for both calls"
    end
end

function test_create_deploy_key_posts_to_only_the_passed_repo
    # The Forgejo-side scoping guarantee: __llm_forgejo_create_deploy_key
    # POSTs via curl to `$__llm_forgejo_url/api/v1/repos/<repo>/keys`. We
    # stub curl on PATH to record argv (URL included), set the global
    # connection state directly to skip the resolve step, and assert the
    # captured URL targets exactly the repo we asked for.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    set -l log "$tmp/curl-argv.log"
    echo '#!/usr/bin/env fish' > "$tmp/bin/curl"
    echo "echo \"\$argv\" >> '$log'" >> "$tmp/bin/curl"
    # Forgejo create response: parse-key-id will read this and emit "999".
    echo 'echo "{\"id\": 999}"' >> "$tmp/bin/curl"
    echo 'exit 0' >> "$tmp/bin/curl"
    chmod +x "$tmp/bin/curl"
    set -l body "
        set -x PATH '$tmp/bin' \$PATH
        source '$SCRIPT'
        # Skip the connection-resolve step by populating the globals
        # __llm_forgejo_api reads. Real value of $__llm_forgejo_url normally
        # comes from instance-profile lookup.
        set -g __llm_forgejo_url 'https://forgejo.example.com'
        set -g __llm_forgejo_token 'fake'
        set -l id (__llm_forgejo_create_deploy_key owner-a/repo-a 'title' 'ssh-ed25519 AAAA fake' ro)
        echo \"id=\$id\"
    "
    command fish -c "$body" >/dev/null 2>&1
    if not test -f "$log"
        __test_record_fail "curl stub logged at least one call" "no log file"
        return
    end
    set -l logged (cat "$log")
    assert_contains "POST URL contains repos/owner-a/repo-a/keys" \
        "$logged" "repos/owner-a/repo-a/keys"
    assert_not_contains "no foreign-repo URL in argv" "$logged" "repo-b"
    # Defensive: must hit api/v1, not some other versioned path that
    # might be cross-instance. (Forgejo has only /api/v1 today.)
    assert_contains "POST URL hits /api/v1" "$logged" "/api/v1/"
end

function test_install_ssh_config_alias_collides_across_repos_with_default_host_prefix
    # Same alias-collision documentation test as in test_slop_gh_key.fish.
    # Within a single Forgejo instance, two installs with different repo
    # slugs but the default host_prefix `forgejo-llm` produce two
    # `Host forgejo-llm-ro` blocks; SSH uses the first match and silently
    # shadows the second repo's key. Per-repo --host-prefix is the
    # current escape hatch.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/keys"
    touch "$tmp/keys/ro_a" "$tmp/keys/rw_a" "$tmp/keys/ro_b" "$tmp/keys/rw_b"
    set -l body "
        source '$SCRIPT'
        set LLM_FORGEJO_KEY_DIR '$tmp/keys'
        __llm_forgejo_install_config owner/repo-a name-a forgejo.example.com \
            '$tmp/keys/ro_a' '$tmp/keys/rw_a' forgejo-llm
        or exit 1
        __llm_forgejo_install_config owner/repo-b name-b forgejo.example.com \
            '$tmp/keys/ro_b' '$tmp/keys/rw_b' forgejo-llm
        or exit 1
    "
    command fish -c "$body" >/dev/null 2>&1
    if not test -f "$tmp/keys/config"
        __test_record_fail "forgejo ssh config file written" "no config file"
        return
    end
    set -l ro_count (grep -c '^Host forgejo-llm-ro$' "$tmp/keys/config")
    assert_eq "two installs → two duplicate Host forgejo-llm-ro blocks" "$ro_count" "2"
    set -l rw_count (grep -c '^Host forgejo-llm-rw$' "$tmp/keys/config")
    assert_eq "two installs → two duplicate Host forgejo-llm-rw blocks" "$rw_count" "2"
    set -l markers (grep -c '^# BEGIN slop-forgejo-key:' "$tmp/keys/config")
    assert_eq "each install left its own marker block" "$markers" "2"
end

run_tests_in_file (basename (status filename))
