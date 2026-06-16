#!/usr/bin/env fish

# Tests for scripts/_py/llm_*.py helpers.
# These are invoked via `uv run --script` so the PEP-723 inline metadata
# governs the Python version, matching how the fish wrappers call them.

source (dirname (status filename))/helpers.fish

set -g GH_PY "$REPO_ROOT/scripts/_py/llm_github_keys.py"
set -g FORGEJO_PY "$REPO_ROOT/scripts/_py/llm_forgejo_keys.py"

function __uv_runs
    if not command -sq uv
        return 1
    end
end

# --- llm_github_keys.py ---

function test_gh_ttl_to_iso_valid
    __uv_runs; or begin
        __test_record_pass "gh ttl-to-iso (skipped: uv not installed)"
        return 0
    end
    set -l out (uv run --script $GH_PY ttl-to-iso 24h 2>&1)
    set -l rc $status
    assert_status "gh ttl-to-iso status" $rc 0
    # Output should look like: 2026-05-02T01:23:45Z
    if string match -rq '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$' -- "$out"
        __test_record_pass "gh ttl-to-iso ISO format"
    else
        __test_record_fail "gh ttl-to-iso ISO format" "got '$out'"
    end
end

function test_gh_ttl_to_iso_invalid
    __uv_runs; or return 0
    set -l out (uv run --script $GH_PY ttl-to-iso bogus 2>&1)
    set -l rc $status
    assert_eq "gh ttl-to-iso invalid fails" $rc 1
end

function test_gh_filter_by_title
    __uv_runs; or return 0
    set -l json '[{"id":1,"title":"llm-agent:ro:s1"},{"id":2,"title":"unrelated"}]'
    set -l out (echo $json | uv run --script $GH_PY filter-by-title '^llm-agent' 2>&1)
    set -l rc $status
    assert_status "gh filter-by-title status" $rc 0
    assert_eq "gh filter-by-title selects matching id" "$out" "1"
end

function test_gh_filter_expired
    __uv_runs; or return 0
    set -l json '[{"id":11,"title":"llm:ro:exp=2024-01-01T00:00:00Z"},{"id":22,"title":"llm:rw:exp=2099-01-01T00:00:00Z"}]'
    set -l out (echo $json | uv run --script $GH_PY filter-expired 2>&1)
    set -l rc $status
    assert_status "gh filter-expired status" $rc 0
    assert_eq "gh filter-expired returns expired only" "$out" "11"
end

function test_gh_ssh_config_uninstall
    __uv_runs; or return 0
    set -l tmp (mk_tmpdir)
    set -l cfg "$tmp/config"
    printf "%s\n" \
        "Host other" \
        "  HostName foo" \
        "" \
        "# BEGIN slop-gh-key:owner-repo:s1:20260101T000000Z" \
        "Host github-llm-ro" \
        "  HostName github.com" \
        "# END slop-gh-key:owner-repo:s1:20260101T000000Z" \
        "" \
        "Host yet-another" \
        "  HostName bar" >$cfg
    set -l out (uv run --script $GH_PY ssh-config-uninstall $cfg '^slop-gh-key:owner-repo:s1:' 2>&1)
    set -l rc $status
    set -l content (cat $cfg)
    assert_status "gh ssh-config-uninstall status" $rc 0
    # First line of the multi-line output is the count of removed blocks.
    # fish already split command substitution on newlines, so $out is a list.
    assert_eq "gh ssh-config-uninstall removed-count is 1" "$out[1]" "1"
    assert_not_contains "gh ssh-config-uninstall stripped block" "$content" "github-llm-ro"
    assert_contains "gh ssh-config-uninstall preserved other host" "$content" "Host other"
    assert_contains "gh ssh-config-uninstall preserved yet-another" "$content" "yet-another"
end

# --- llm_forgejo_keys.py ---

function test_forgejo_host_from_url
    __uv_runs; or return 0
    set -l out (uv run --script $FORGEJO_PY host-from-url 'https://forgejo.example.com:8080/path' 2>&1)
    set -l rc $status
    assert_status "forgejo host-from-url status" $rc 0
    assert_eq "forgejo host-from-url result" "$out" "forgejo.example.com"
end

function test_forgejo_make_payload_ro
    __uv_runs; or return 0
    set -l out (uv run --script $FORGEJO_PY make-payload "title-ro" "ssh-ed25519 AAA" "true" 2>&1)
    set -l rc $status
    assert_status "forgejo make-payload status" $rc 0
    assert_contains "forgejo make-payload includes title" "$out" "\"title\": \"title-ro\""
    assert_contains "forgejo make-payload sets read_only=true" "$out" "\"read_only\": true"
end

function test_forgejo_make_payload_rw
    __uv_runs; or return 0
    set -l out (uv run --script $FORGEJO_PY make-payload "title-rw" "ssh-ed25519 BBB" "false" 2>&1)
    assert_contains "forgejo make-payload sets read_only=false for rw" "$out" "\"read_only\": false"
end

function test_forgejo_instance_lifecycle
    __uv_runs; or return 0
    set -l tmp (mk_tmpdir)
    set -l cfg "$tmp/inst.json"
    echo '{"instances":{}}' >$cfg

    uv run --script $FORGEJO_PY instance-set $cfg main 'https://forgejo.example.com' FORGEJO_TOKEN_MAIN 2>&1 >/dev/null
    assert_status "forgejo instance-set status" $status 0

    set -l listed (uv run --script $FORGEJO_PY instance-list $cfg 2>&1)
    assert_contains "forgejo instance-list shows main" "$listed" "main"
    assert_contains "forgejo instance-list shows url" "$listed" "https://forgejo.example.com"
    assert_contains "forgejo instance-list shows token_env" "$listed" "FORGEJO_TOKEN_MAIN"

    set -l got (uv run --script $FORGEJO_PY instance-get $cfg main 2>&1)
    assert_eq "forgejo instance-get tab-separated url and env" "$got" "https://forgejo.example.com	FORGEJO_TOKEN_MAIN"

    set -l miss (uv run --script $FORGEJO_PY instance-get $cfg nonexistent 2>&1)
    set -l miss_rc $status
    assert_eq "forgejo instance-get unknown name fails" $miss_rc 1

    uv run --script $FORGEJO_PY instance-remove $cfg main 2>&1 >/dev/null
    set -l after (uv run --script $FORGEJO_PY instance-list $cfg 2>&1)
    assert_contains "forgejo instance-remove drops the entry" "$after" "No Forgejo instance profiles configured"

end

function test_forgejo_parse_key_id
    __uv_runs; or return 0
    set -l out (echo '{"id":42,"other":"x"}' | uv run --script $FORGEJO_PY parse-key-id 2>&1)
    assert_eq "forgejo parse-key-id" "$out" "42"
end

function test_forgejo_list_keys
    __uv_runs; or return 0
    set -l json '[{"id":1,"read_only":true,"created_at":"2026-01-01T00:00:00Z","title":"a"},{"id":2,"read_only":false,"created_at":"2026-01-02T00:00:00Z","title":"b"}]'
    set -l out (echo $json | uv run --script $FORGEJO_PY list-keys 2>&1)
    assert_contains "forgejo list-keys row 1 is ro" "$out" "1	ro	2026-01-01T00:00:00Z	a"
    assert_contains "forgejo list-keys row 2 is rw" "$out" "2	rw	2026-01-02T00:00:00Z	b"
end

run_tests_in_file (basename (status filename))
