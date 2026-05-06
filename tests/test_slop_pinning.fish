#!/usr/bin/env fish

# Tests for scripts/slop-pinning.fish
# - help path
# - passes against real repo fixtures (current state must be pinned)
# - fails when an unpinned `latest` is introduced into a temp fixture

source (dirname (status filename))/helpers.fish

set -g CHECK "$SCRIPTS_DIR/slop-pinning.fish"

function test_dockerfile_pip_lines_carry_break_system_packages
    # Static guard: every `pip install` / `uv pip install --system` line
    # in Dockerfile.agent.tools must carry `--break-system-packages`.
    # The base image is node:22-bookworm whose Python ships PEP 668's
    # externally-managed marker; without the flag the build fails on
    # CI (and on any fresh local rebuild). The pinning gate's job is
    # already to keep this kind of supply-chain knob from drifting.
    set -l df "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools"
    if not test -f "$df"
        __test_record_pass "Dockerfile pip break-system-packages (skipped: file missing)"
        return 0
    end
    # Match RUN-prefixed lines so comments don't trip the assertion;
    # the slop-pinning gate uses the same anchor.
    set -l offending (grep -nE '^RUN .*pip install' "$df" | grep -v -- '--break-system-packages')
    if test (count $offending) -gt 0
        __test_record_fail "all pip install lines carry --break-system-packages" \
            "offending: $offending"
        return
    end
    __test_record_pass "all pip install lines carry --break-system-packages"
end

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

function test_detects_latest_tag_in_slop_cue_image_base
    # Once users start declaring `image: base: "registry/foo:latest"`
    # in slop.cue (the new orchestrator surface), the pinning gate has
    # to flag it the same way it flags `=latest` in env files.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/library/layer/container"
    cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/docker-compose.yml" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env.example"
    # The offending file: a slop.cue at the tmp repo root.
    echo 'package slop
import "slop.dev/isolation/schema"
profiles: bad: schema.#Profile & {
    agent:       "claude"
    environment: "container"
    image: base: "registry.example/foo:latest"
}' > "$tmp/slop.cue"

    set -l saved $PWD
    cd "$tmp"
    set -l out (run_fish $CHECK 2>&1)
    set -l rc $status
    cd "$saved"

    assert_eq "slop-pinning fails on :latest in slop.cue" $rc 1
    assert_contains "slop-pinning names the slop.cue location" "$out" "slop.cue"
    assert_contains "slop-pinning flags the latest tag" "$out" ":latest"
end

function test_detects_at_latest_in_extra_npm
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/library/layer/container"
    cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/docker-compose.yml" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env.example"
    echo 'package slop
import "slop.dev/isolation/schema"
profiles: bad: schema.#Profile & {
    agent:       "claude"
    environment: "container"
    image: "extra-npm": ["@anthropic-ai/sdk@latest"]
}' > "$tmp/slop.cue"

    set -l saved $PWD
    cd "$tmp"
    set -l out (run_fish $CHECK 2>&1)
    set -l rc $status
    cd "$saved"

    assert_eq "slop-pinning fails on @latest in extra-npm" $rc 1
    assert_contains "slop-pinning flags @latest" "$out" "@latest"
end

function test_detects_double_eq_latest_in_extra_pip
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/library/layer/container"
    cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/docker-compose.yml" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env.example"
    echo 'package slop
import "slop.dev/isolation/schema"
profiles: bad: schema.#Profile & {
    agent:       "claude"
    environment: "container"
    image: "extra-pip": ["ruff==latest"]
}' > "$tmp/slop.cue"

    set -l saved $PWD
    cd "$tmp"
    set -l out (run_fish $CHECK 2>&1)
    set -l rc $status
    cd "$saved"

    assert_eq "slop-pinning fails on ==latest in extra-pip" $rc 1
    assert_contains "slop-pinning flags ==latest" "$out" "==latest"
end

function test_passes_against_pinned_slop_cue
    # A slop.cue with properly pinned versions must NOT trip the
    # check. Uses the bundled sample's shape (no image extras) plus
    # a fancy second profile with concrete pinned versions, to make
    # sure pinned `extra-pip`/`extra-npm` entries aren't false-flagged.
    set -l tmp (mk_tmpdir)
    mkdir -p "$tmp/library/layer/container"
    cp "$REPO_ROOT/library/layer/container/Dockerfile.agent.tools" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/docker-compose.yml" "$tmp/library/layer/container/"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env"
    cp "$REPO_ROOT/library/layer/container/agent-tools.env.example" "$tmp/library/layer/container/agent-tools.env.example"
    echo 'package slop
import "slop.dev/isolation/schema"
profiles: pinned: schema.#Profile & {
    agent:       "claude"
    environment: "container"
    image: {
        base: "local/agent-sandbox-tools:slop-abc123"
        "extra-pip": ["ruff==0.6.0", "mypy==1.10.0"]
        "extra-npm": ["gh@2.0.0", "@anthropic-ai/sdk@0.30.0"]
    }
}' > "$tmp/slop.cue"

    set -l saved $PWD
    cd "$tmp"
    set -l out (run_fish $CHECK 2>&1)
    set -l rc $status
    cd "$saved"

    assert_status "slop-pinning passes on pinned slop.cue" $rc 0
    assert_contains "slop-pinning success message" "$out" "pinning check passed"
end

run_tests_in_file (basename (status filename))
