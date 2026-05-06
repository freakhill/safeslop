#!/usr/bin/env fish

# Golden-file diff harness for slop-isolate.
#
# For every preset × applicable adapter, compile the preset to a tmpdir and
# diff against the checked-in fixture. Drift fails the test. Update with:
#   uv run --script scripts/_py/isolation.py compile --preset <name> \
#     --out library/layer/policy/fixtures/<name>

source (dirname (status filename))/helpers.fish

set -g ISOLATION_PY "$SCRIPTS_DIR/_py/isolation.py"
set -g FIXTURES_DIR "$REPO_ROOT/library/layer/policy/fixtures"

set -g PRESETS \
    any-agent \
    claude-code \
    opencode \
    crewai \
    pydantic-ai \
    ag2 \
    openclaw \
    zeroclaw \
    nous-hermes-local \
    nous-hermes-remote

function __have_cue
    command -sq cue; and command -sq uv
end

function __diff_preset --argument-names preset
    set -l tmp (mk_tmpdir)
    uv run --script "$ISOLATION_PY" compile --preset "$preset" --out "$tmp" >/dev/null 2>&1
    set -l rc $status
    if test $rc -ne 0
        __test_record_fail "fixture-compile $preset" "compile exited $rc"
        return 1
    end

    set -l fixture_dir "$FIXTURES_DIR/$preset"
    if not test -d "$fixture_dir"
        __test_record_fail "fixture-compile $preset" "no fixture dir at $fixture_dir"
        return 1
    end

    set -l drift 0
    for f in $tmp/*
        set -l basename (basename $f)
        set -l checked "$fixture_dir/$basename"
        if not test -f "$checked"
            __test_record_fail "fixture-compile $preset" "missing fixture: $basename"
            set drift 1
            continue
        end
        if not diff -q "$f" "$checked" >/dev/null
            diff -u "$checked" "$f" 1>&2
            __test_record_fail "fixture-compile $preset" "drift: $basename"
            set drift 1
        end
    end
    if test $drift -eq 0
        __test_record_pass "fixture-compile $preset"
    end
end

function test_fixtures_match_checked_in
    if not __have_cue
        __test_record_pass "fixture diff (skipped: cue/uv missing)"
        return 0
    end
    for preset in $PRESETS
        __diff_preset $preset
    end
end

run_tests_in_file
