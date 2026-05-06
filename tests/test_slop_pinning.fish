#!/usr/bin/env fish

# Tests for scripts/slop-pinning.fish
# - help path
# - passes against real repo fixtures (current state must be pinned)
# - fails when an unpinned `latest` is introduced into a temp fixture

source (dirname (status filename))/helpers.fish

set -g CHECK "$SCRIPTS_DIR/slop-pinning.fish"

function test_help_flag_works
    set -l out (run_fish $CHECK --help 2>&1)
    set -l rc $status
    assert_status "slop-pinning --help status" $rc 0
    assert_contains "slop-pinning --help output" "$out" "Usage:"
    assert_contains "slop-pinning --help output mentions Checks" "$out" "Checks:"
end

function test_help_subcommand_works
    set -l out (run_fish $CHECK help 2>&1)
    set -l rc $status
    assert_status "slop-pinning help status" $rc 0
    assert_contains "slop-pinning help output" "$out" "Usage:"
end

function test_help_includes_enriched_sections
    set -l out (run_fish $CHECK help 2>&1)
    assert_contains "slop-pinning help has Description" "$out" "Description:"
    assert_contains "slop-pinning help has Examples" "$out" "Examples"
end

function test_unknown_arg_fails_with_help
    set -l out (run_fish $CHECK bogus 2>&1)
    set -l rc $status
    assert_eq "slop-pinning unknown arg fails" $rc 1
    assert_contains "slop-pinning unknown arg shows Usage" "$out" "Usage:"
end

function test_passes_against_repo_fixtures
    # library/layer/container/agent-tools.env is gitignored (it's a copy of .example for local
    # use). Make the happy path hermetic by staging all four required files in
    # a tmp dir, using the .example contents to seed the .env file.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/library/layer/container"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env.example"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env"
    cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools"   "$tmp/library/layer/container/Dockerfile.agent.tools"
    cp "$REPO_ROOT/library/layer/container/docker-compose.yml"       "$tmp/library/layer/container/docker-compose.yml"

    set -l saved $PWD
    cd "$tmp"
    set -l out (run_fish $CHECK 2>&1)
    set -l rc $status
    cd "$saved"

    assert_status "slop-pinning passes on staged fixtures" $rc 0
    assert_contains "slop-pinning success message" "$out" "pinning check passed"
end

function test_detects_unpinned_latest_in_env
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/library/layer/container"
    # Copy real reference files to keep the rest of the check satisfied.
    cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/docker-compose.yml" "$tmp/library/layer/container/"
    # Introduce an unpinned `latest` line in the env file fixture.
    echo "CLAUDE_CODE_VERSION=latest" > "$tmp/library/layer/container/agent-tools.env"
    echo "CLAUDE_CODE_VERSION=latest" > "$tmp/library/layer/container/agent-tools.env.example"

    set -l saved $PWD
    cd "$tmp"
    set -l out (run_fish $CHECK 2>&1)
    set -l rc $status
    cd "$saved"

    assert_eq "slop-pinning fails on latest" $rc 1
    assert_contains "slop-pinning reports failure reason" "$out" "unpinned"
end

function test_detects_unpinned_latest_for_openclaw_and_zeroclaw
    # Why: OpenClaw / ZeroClaw env slots were added as reserved templates.
    # If a user uncomments and sets `=latest`, the pinning gate must catch it.
    for var in OPENCLAW_VERSION ZEROCLAW_VERSION
        set -l tmp (mk_tmpdir)
        mkdir -p "$tmp/library/layer/container"
        cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools" "$tmp/library/layer/container/"
        cp "$REPO_ROOT/library/layer/container/docker-compose.yml" "$tmp/library/layer/container/"
        echo "$var=latest" > "$tmp/library/layer/container/agent-tools.env"
        echo "$var=latest" > "$tmp/library/layer/container/agent-tools.env.example"

        set -l saved $PWD
        cd "$tmp"
        set -l out (run_fish $CHECK 2>&1)
        set -l rc $status
        cd "$saved"

        assert_eq "slop-pinning fails on $var=latest" $rc 1
        assert_contains "slop-pinning flags $var" "$out" "unpinned"
    end
end

run_tests_in_file (basename (status filename))
