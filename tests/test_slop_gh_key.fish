#!/usr/bin/env fish

# Tests for scripts/slop-gh-key.fish — sourced module.
# We do not call the real GitHub API; we exercise help and arg-validation paths.

source (dirname (status filename))/helpers.fish

set -g SCRIPT "$SCRIPTS_DIR/slop-gh-key.fish"

function __invoke
    command fish -c "source '$SCRIPT'; slop-gh-key $argv" 2>&1
end

function test_no_args_prints_usage_and_fails
    set -l out (__invoke)
    set -l rc $status
    assert_eq "slop-gh-key no-args fails" $rc 1
    assert_contains "slop-gh-key no-args mentions Usage" "$out" "Usage:"
end

function test_help_flag
    set -l out (__invoke --help)
    set -l rc $status
    assert_status "slop-gh-key --help status" $rc 0
    assert_contains "slop-gh-key --help mentions Usage" "$out" "Usage:"
    assert_contains "slop-gh-key --help mentions create-pair" "$out" "create-pair"
end

function test_unknown_argument_fails
    set -l out (__invoke list --bogus)
    set -l rc $status
    assert_eq "slop-gh-key unknown arg fails" $rc 1
    assert_contains "slop-gh-key unknown arg message" "$out" "Unknown argument"
end

function test_unknown_command_fails
    set -l out (__invoke do-not-exist)
    set -l rc $status
    assert_eq "slop-gh-key unknown cmd fails" $rc 1
    assert_contains "slop-gh-key unknown cmd message" "$out" "Unknown command"
end

function test_create_requires_repo_and_access
    set -l out (__invoke create)
    set -l rc $status
    # Without gh installed the require_tools check may fire first; both are valid
    # validation paths. Either way, exit must be non-zero with a clear message.
    assert_eq "slop-gh-key create missing args fails" $rc 1
end

function test_print_ssh_config_validates
    set -l out (__invoke print-ssh-config)
    set -l rc $status
    assert_eq "slop-gh-key print-ssh-config no args fails" $rc 1
    assert_contains "slop-gh-key print-ssh-config error mentions ro-key" "$out" "--ro-key"
end

function test_print_ssh_config_renders_aliases
    set -l tmp (mk_tmpdir)
    set -l ro "$tmp/ro_key"
    set -l rw "$tmp/rw_key"
    touch $ro $rw
    set -l out (__invoke print-ssh-config --ro-key $ro --rw-key $rw)
    set -l rc $status
    assert_status "slop-gh-key print-ssh-config status" $rc 0
    assert_contains "slop-gh-key print-ssh-config ro alias" "$out" "github-llm-ro"
    assert_contains "slop-gh-key print-ssh-config rw alias" "$out" "github-llm-rw"
    assert_contains "slop-gh-key print-ssh-config has IdentitiesOnly" "$out" "IdentitiesOnly yes"
end

function test_uninstall_ssh_config_requires_marker_or_repo
    set -l out (__invoke uninstall-ssh-config)
    set -l rc $status
    assert_eq "slop-gh-key uninstall-ssh-config no args fails" $rc 1
    assert_contains "slop-gh-key uninstall-ssh-config message" "$out" "marker"
end

function test_help_advertises_here_and_tui
    set -l out (__invoke --help)
    assert_contains "slop-gh-key help mentions here" "$out" "here create-pair"
    assert_contains "slop-gh-key help mentions tui" "$out" "slop-gh-key tui"
end

function test_here_requires_subcommand
    set -l out (__invoke here)
    set -l rc $status
    assert_eq "slop-gh-key here no-sub fails" $rc 1
    assert_contains "slop-gh-key here no-sub message" "$out" "requires a subcommand"
end

function test_here_unknown_subcommand_fails
    # Run from a tmpdir that we initialize as a git repo with a github origin
    # so the inference step succeeds and the unknown-sub check is what fires.
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        command git remote add origin git@github.com:owner/repo.git
        source '$SCRIPT'
        slop-gh-key here totally-not-a-thing
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-gh-key here unknown-sub fails" $rc 1
    assert_contains "slop-gh-key here unknown-sub message" "$out" "unknown 'here' subcommand"
end

function test_here_outside_git_repo_fails_clearly
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        source '$SCRIPT'
        slop-gh-key here list
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-gh-key here outside-repo fails" $rc 1
    assert_contains "slop-gh-key here outside-repo message" "$out" "could not infer GitHub repo"
end

function test_repo_inference_supports_url_forms
    # Each form should yield owner/repo via __llm_gh_repo_from_git.
    for url in \
        "git@github.com:owner/repo.git" \
        "git@github.com:owner/repo" \
        "https://github.com/owner/repo.git" \
        "https://github.com/owner/repo" \
        "ssh://git@github.com/owner/repo.git" \
        "git@github-llm-rw:owner/repo.git"
        set -l tmp (mk_tmpdir)
        set -l body "
            cd '$tmp'
            command git init -q
            command git remote add origin '$url'
            source '$SCRIPT'
            __llm_gh_repo_from_git
        "
        set -l out (command fish -c "$body" 2>&1)
        assert_eq "slop-gh-key infer from $url" "$out" "owner/repo"
    end
end

function test_repo_inference_rejects_non_github
    set -l tmp (mk_tmpdir)
    set -l body "
        cd '$tmp'
        command git init -q
        command git remote add origin git@gitlab.com:owner/repo.git
        source '$SCRIPT'
        __llm_gh_repo_from_git
        echo \"rc=\$status\"
    "
    set -l out (command fish -c "$body" 2>&1)
    assert_contains "slop-gh-key rejects gitlab origin" "$out" "rc=1"
end

function test_generate_key_returns_zero_on_success
    # Root-cause regression: `__llm_gh_generate_key` used to return 1 on a
    # fully successful keygen, because its last command was
    #   set -g __llm_gh_last_key_pub (string trim -- (string collect < pub))
    # `string collect < file` strips one trailing newline, and `string trim`
    # then has nothing to trim and exits 1. That 1 propagated through
    # `set -g` and made the whole function report failure. Downstream,
    # __llm_gh_create_one bailed before calling gh api, never attempted
    # the RW upload, and leaked an orphan ed25519 keypair on each retry —
    # exactly matching the user-reported "Create + List shows nothing"
    # symptom (many _ro_ files in ~/.ssh, zero keys on github.com).
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/keys"
    set -l body "
        source '$SCRIPT'
        set LLM_GH_KEY_DIR '$tmp/keys'
        __llm_gh_generate_key ro test 2026-05-06T00:00:00Z
        exit \$status
    "
    command fish -c "$body" >/dev/null 2>&1
    set -l rc $status
    assert_status "__llm_gh_generate_key returns 0 on success" $rc 0
end

function test_create_one_cleans_up_local_keys_on_upload_failure
    # Regression: when `gh api POST .../keys` fails, the locally generated
    # ed25519 keypair was being left behind in $LLM_GH_KEY_DIR. Each retry
    # then created a fresh orphan, ending up with N _ro_ files on disk and
    # zero keys on GitHub — the user-visible "create + list shows nothing"
    # bug. The fix: rm -f the key files on upload failure so disk state
    # tracks GitHub state.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin" "$tmp/keys"
    # Stub gh to: succeed for `auth status` (so require_tools passes), and
    # fail with non-zero exit on the POST call. ssh-keygen and uv are real.
    echo '#!/usr/bin/env fish' > "$tmp/bin/gh"
    echo 'if test "$argv[1]" = "auth"' >> "$tmp/bin/gh"
    echo '    exit 0' >> "$tmp/bin/gh"
    echo 'end' >> "$tmp/bin/gh"
    echo 'echo "{\"message\": \"stub: simulated 422\"}" 1>&2' >> "$tmp/bin/gh"
    echo 'exit 22' >> "$tmp/bin/gh"
    chmod +x "$tmp/bin/gh"
    set -l body "
        set -x PATH '$tmp/bin' \$PATH
        source '$SCRIPT'
        set LLM_GH_KEY_DIR '$tmp/keys'
        __llm_gh_create_one owner/repo ro 24h test
    "
    command fish -c "$body" >/dev/null 2>&1
    # No leftover key files: that is the regression we are testing.
    set -l leftovers (count $tmp/keys/llm_agent_github_*)
    assert_eq "no orphan keys left after upload failure" $leftovers 0
end

# ---------------------------------------------------------------------------
# Repo-uniqueness checks. Three layers — GitHub API, on-disk filename, ssh
# config alias — have very different scoping properties. These tests pin
# down what is actually repo-unique today vs. what users have to guard
# against manually. See plans/are-the-keys-we-quiet-music.md for context.
# ---------------------------------------------------------------------------

function test_generate_key_filenames_distinct_across_names
    # Two consecutive __llm_gh_generate_key calls into the same key dir
    # with different --name values must produce distinct on-disk paths and
    # distinct ed25519 public-key bytes. This is the basic "two repos → two
    # files" guarantee, even when both run in the same UTC second.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/keys"
    set -l body "
        source '$SCRIPT'
        set LLM_GH_KEY_DIR '$tmp/keys'
        __llm_gh_generate_key ro repo-a 2026-05-06T00:00:00Z
        or exit 1
        echo \"path_a=\$__llm_gh_last_key_path\"
        echo \"pub_a=\$__llm_gh_last_key_pub\"
        __llm_gh_generate_key ro repo-b 2026-05-06T00:00:00Z
        or exit 1
        echo \"path_b=\$__llm_gh_last_key_path\"
        echo \"pub_b=\$__llm_gh_last_key_pub\"
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l path_a (string match -r 'path_a=(.+)' -- $out)[2]
    set -l pub_a (string match -r 'pub_a=(.+)' -- $out)[2]
    set -l path_b (string match -r 'path_b=(.+)' -- $out)[2]
    set -l pub_b (string match -r 'pub_b=(.+)' -- $out)[2]
    if test -z "$path_a" -o -z "$path_b"
        __test_record_fail "generate_key produced both paths" "out=$out"
        return
    end
    if test "$path_a" != "$path_b"
        __test_record_pass "two names → distinct on-disk filenames"
    else
        __test_record_fail "two names → distinct on-disk filenames" \
            "both calls returned $path_a"
    end
    if test "$pub_a" != "$pub_b"
        __test_record_pass "two names → distinct ed25519 public keys"
    else
        __test_record_fail "two names → distinct ed25519 public keys" \
            "ssh-keygen produced the same public key for both calls"
    end
end

function test_create_deploy_key_posts_to_only_the_passed_repo
    # The GitHub-side scoping guarantee: a deploy key created via
    # `__llm_gh_create_deploy_key owner-a/repo-a ...` must POST to
    # `repos/owner-a/repo-a/keys` and nowhere else. GitHub binds the key
    # to that single repo; without this, "repo-unique on the server" is
    # just a story we tell ourselves. Stub gh to record its argv to a
    # file, then assert the recorded URL.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin" "$tmp/keys"
    set -l log "$tmp/gh-argv.log"
    echo '#!/usr/bin/env fish' > "$tmp/bin/gh"
    echo 'if test "$argv[1]" = "auth"' >> "$tmp/bin/gh"
    echo '    exit 0' >> "$tmp/bin/gh"
    echo 'end' >> "$tmp/bin/gh"
    # Log every non-auth invocation, then emit a fake id so create_one is
    # happy and proceeds normally instead of bailing on upload failure.
    echo "echo \"\$argv\" >> '$log'" >> "$tmp/bin/gh"
    echo 'echo "999"' >> "$tmp/bin/gh"
    echo 'exit 0' >> "$tmp/bin/gh"
    chmod +x "$tmp/bin/gh"
    set -l body "
        set -x PATH '$tmp/bin' \$PATH
        source '$SCRIPT'
        set LLM_GH_KEY_DIR '$tmp/keys'
        __llm_gh_create_one owner-a/repo-a ro 24h test
    "
    command fish -c "$body" >/dev/null 2>&1
    if not test -f "$log"
        __test_record_fail "gh stub logged at least one call" "no log file"
        return
    end
    set -l logged (cat "$log")
    assert_contains "POST targets owner-a/repo-a" "$logged" "repos/owner-a/repo-a/keys"
    # Defense against future refactors where the call might fan out to a
    # broader endpoint. No mention of a foreign repo in the recorded argv.
    assert_not_contains "no foreign-repo POST in argv" "$logged" "repo-b"
    assert_not_contains "no all-repos endpoint in argv" "$logged" "/user/keys"
end

function test_install_ssh_config_alias_collides_across_repos_with_default_host_prefix
    # Documents an existing limitation: __llm_gh_install_config writes
    # `Host <host_prefix>-ro` / `<host_prefix>-rw` blocks. With the
    # default host_prefix (`github-llm`), two installs from two different
    # repos append two separate `Host github-llm-ro` blocks to the same
    # ~/.ssh/config — and SSH uses the FIRST matching `Host` block, so the
    # second repo's key is silently shadowed. Users must pass a per-repo
    # --host-prefix today; this test makes that gap visible and named so
    # we can decide later whether `here create-pair` should auto-derive
    # a slug-suffixed prefix. If we ever do, this test flips its
    # assertion to "aliases differ across repos".
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/keys"
    # Both keypairs are dummy (touch is enough; install-ssh-config only
    # checks file existence, not key validity).
    touch "$tmp/keys/ro_a" "$tmp/keys/rw_a" "$tmp/keys/ro_b" "$tmp/keys/rw_b"
    set -l body "
        source '$SCRIPT'
        set LLM_GH_KEY_DIR '$tmp/keys'
        __llm_gh_install_config owner/repo-a name-a \
            '$tmp/keys/ro_a' '$tmp/keys/rw_a' github-llm
        or exit 1
        __llm_gh_install_config owner/repo-b name-b \
            '$tmp/keys/ro_b' '$tmp/keys/rw_b' github-llm
        or exit 1
    "
    command fish -c "$body" >/dev/null 2>&1
    if not test -f "$tmp/keys/config"
        __test_record_fail "ssh config file written" "no config file"
        return
    end
    # Two `Host github-llm-ro` blocks: the canonical signal of the alias
    # collision. If this ever drops to 1 (= the second install replaced
    # the first) or stays at 2 across repos with no per-repo
    # differentiation, the ambiguity is real.
    set -l ro_count (grep -c '^Host github-llm-ro$' "$tmp/keys/config")
    assert_eq "two installs → two duplicate Host github-llm-ro blocks" "$ro_count" "2"
    set -l rw_count (grep -c '^Host github-llm-rw$' "$tmp/keys/config")
    assert_eq "two installs → two duplicate Host github-llm-rw blocks" "$rw_count" "2"
    # Marker blocks ARE per-repo (slug + name + stamp), so uninstall can
    # still target them — confirm both markers are present.
    set -l markers (grep -c '^# BEGIN slop-gh-key:' "$tmp/keys/config")
    assert_eq "each install left its own marker block" "$markers" "2"
end

function test_list_header_uses_real_tabs
    # Regression: `echo "id\taccess..."` in fish prints LITERAL backslash-t
    # because fish echo doesn't interpret escapes. The body rows (from
    # `gh api --jq`) emit real tabs, so a backslash-t header destroys
    # column alignment. Must use printf. Reproduce by inspecting the
    # bytes of the header line — we don't need network because the header
    # prints before any gh api call when --repo is missing? No, repo is
    # required, so we have to feed a stub repo. Instead grep the source
    # for the printf — a static check is enough to guard the regression.
    set -l content (cat "$SCRIPT")
    assert_contains "list header uses printf for real tabs" "$content" \
        "printf 'id\\taccess\\tcreated_at\\ttitle"
    assert_not_contains "list header no longer echoes literal backslash-t" "$content" \
        'echo "id\taccess'
end

function test_tui_without_gum_prints_install_hint
    # Force a PATH that excludes gum so the soft-dep check fires regardless of
    # whether the developer has gum installed.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/bin"
    # Provide minimal stand-ins for the few tools the function calls *before*
    # the gum check (none should be needed, but give it a clean stub PATH).
    set -l body "
        set -x PATH '$tmp/bin'
        source '$SCRIPT'
        slop-gh-key tui
    "
    set -l out (command fish -c "$body" 2>&1)
    set -l rc $status
    assert_eq "slop-gh-key tui no-gum fails" $rc 1
    assert_contains "slop-gh-key tui no-gum mentions gum" "$out" "gum"
    assert_contains "slop-gh-key tui no-gum suggests brew install" "$out" "brew install gum"
end

run_tests_in_file (basename (status filename))
