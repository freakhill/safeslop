#!/usr/bin/env fish

# Schema-level tests for the slop.cue orchestrator (Phase C).
#
# The runtime that consumes this schema lands in Phase D
# (scripts/_py/slop_orchestrator.py). This file just locks the contract
# down: the schema parses, the bundled sample validates, and a handful
# of bad-shape inputs fail validation with a recognizable error.

source (dirname (status filename))/helpers.fish

set -g POLICY_DIR "$REPO_ROOT/library/layer/policy"
set -g SAMPLE_SLOP "$POLICY_DIR/samples/slop/slop.cue"
set -g SAMPLE_ISO  "$POLICY_DIR/samples/isolation/user-config.cue"

function __have_cue
    command -sq cue
end

function test_policy_module_vets_clean
    if not __have_cue
        __test_record_pass "policy module cue-vets (skipped: cue missing)"
        return 0
    end
    set -l saved $PWD
    cd "$POLICY_DIR"
    set -l out (cue vet ./... 2>&1)
    set -l rc $status
    cd "$saved"
    if test $rc -ne 0
        __test_record_fail "policy module cue-vets clean" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "policy module cue-vets clean"
end

function test_schema_exposes_orchestrator_types
    # Static guard against the schema regressing — the orchestrator
    # implementation in Phase D (slop_orchestrator.py) will refer to
    # these types by name when validating user slop.cue files.
    set -l content (cat "$POLICY_DIR/schema/schema.cue")
    for ty in "#Profile" "#Agent" "#Environment" "#Credentials" \
              "#OnExitHook" "#ImageSpec" "#Slop"
        assert_contains "schema declares $ty" "$content" "$ty:"
    end
end

function test_sample_slop_cue_validates_against_schema
    if not __have_cue
        __test_record_pass "sample slop.cue validates (skipped: cue missing)"
        return 0
    end
    set -l saved $PWD
    cd "$POLICY_DIR"
    set -l out (cue vet ./samples/slop/... ./schema/... 2>&1)
    set -l rc $status
    cd "$saved"
    if test $rc -ne 0
        __test_record_fail "sample slop.cue validates" "rc=$rc, out=$out"
        return
    end
    __test_record_pass "sample slop.cue validates"
end

function test_unknown_agent_is_rejected
    # Compile a slop.cue with `agent: "totally-not-an-agent"` and confirm
    # cue vet rejects it. Catches regressions where someone widens the
    # #Agent disjunction to a bare `string` and silently accepts garbage.
    if not __have_cue
        __test_record_pass "unknown agent rejected (skipped: cue missing)"
        return 0
    end
    set -l tmp (mk_tmpdir)
    set -l target "$POLICY_DIR/samples/slop/test-bad-agent.cue"
    register_cleanup_path "$target"
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "evil": schema.#Profile & {
    agent:       "totally-not-an-agent"
    environment: "host"
    isolation:   presets.#OpenCode
}' > "$target"
    set -l saved $PWD
    cd "$POLICY_DIR"
    set -l out (cue vet ./samples/slop/... ./schema/... 2>&1)
    set -l rc $status
    cd "$saved"
    rm -f "$target"
    assert_eq "cue rejects unknown agent" $rc 1
    assert_contains "rejection mentions the offending value" "$out" \
        "totally-not-an-agent"
end

function test_unknown_on_exit_hook_is_rejected
    # The hook list is a closed disjunction; CUE must reject typos.
    if not __have_cue
        __test_record_pass "unknown on-exit hook rejected (skipped: cue missing)"
        return 0
    end
    set -l target "$POLICY_DIR/samples/slop/test-bad-hook.cue"
    register_cleanup_path "$target"
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "demo": schema.#Profile & {
    agent:       "claude"
    environment: "host"
    isolation:   presets.#ClaudeCode
    "on-exit":   ["delete-everything"]
}' > "$target"
    set -l saved $PWD
    cd "$POLICY_DIR"
    set -l out (cue vet ./samples/slop/... ./schema/... 2>&1)
    set -l rc $status
    cd "$saved"
    rm -f "$target"
    assert_eq "cue rejects unknown on-exit hook" $rc 1
    assert_contains "rejection mentions the offending value" "$out" \
        "delete-everything"
end

function test_unknown_environment_is_rejected
    if not __have_cue
        __test_record_pass "unknown environment rejected (skipped: cue missing)"
        return 0
    end
    set -l target "$POLICY_DIR/samples/slop/test-bad-env.cue"
    register_cleanup_path "$target"
    echo 'package slop
import "slop.dev/isolation/schema"
import "slop.dev/isolation/presets"
profiles: "demo": schema.#Profile & {
    agent:       "claude"
    environment: "kubernetes"
    isolation:   presets.#ClaudeCode
}' > "$target"
    set -l saved $PWD
    cd "$POLICY_DIR"
    set -l out (cue vet ./samples/slop/... ./schema/... 2>&1)
    set -l rc $status
    cd "$saved"
    rm -f "$target"
    assert_eq "cue rejects unknown environment" $rc 1
    assert_contains "rejection mentions the offending value" "$out" "kubernetes"
end

run_tests_in_file (basename (status filename))
