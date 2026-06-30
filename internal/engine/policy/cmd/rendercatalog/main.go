// rendercatalog is a build-time tool (specs/0059 W2) that renders the authored
// catalog.cue into the embedded catalog.json. It runs in-process via cuelang.org/go —
// no external `cue` binary (specs/0001 §6.1). Invoked by `make render-catalog`; the
// `make check` sync check re-renders to a temp file and diffs against the committed
// catalog.json so a cue/json drift fails CI (mirrors the check-assets pattern).
//
// Usage (from the module root):
//
//	go run ./internal/engine/policy/cmd/rendercatalog
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
)

func main() {
	dir := "internal/engine/policy"
	// catalog.cue references the #Catalog/#Package/#Bundle/#Upstream schema, so both
	// files load as one instance (both `package safeslop`).
	insts := load.Instances([]string{"catalog.cue", "schema/catalog.cue"}, &load.Config{Dir: dir})
	if len(insts) != 1 || insts[0].Err != nil {
		die("loading catalog.cue/schema", insts[0].Err)
	}
	ctx := cuecontext.New()
	val := ctx.BuildInstance(insts[0])
	if val.Err() != nil {
		die("building", val.Err())
	}
	cat := val.LookupPath(cue.ParsePath("catalog"))
	if cat.Err() != nil {
		die("lookup catalog", cat.Err())
	}
	// Concrete(true) forces full evaluation + catches any non-concrete (incomplete)
	// field — a malformed pin fails the render, never reaching the embedded JSON.
	if err := cat.Validate(cue.Concrete(true)); err != nil {
		die("validating against #Catalog", err)
	}
	// Decode into a generic map and re-encode indented, so the committed artifact is
	// diff-friendly (arrays keep their declared order; object keys sort alphabetically).
	var data map[string]any
	if err := cat.Decode(&data); err != nil {
		die("decoding", err)
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		die("marshalling json", err)
	}
	outPath := filepath.Join(dir, "catalog.json")
	if len(os.Args) > 1 {
		outPath = os.Args[1] // a sync-check path (make check-catalog-sync)
	}
	if err := os.WriteFile(outPath, append(out, '\n'), 0o644); err != nil {
		die("writing "+outPath, err)
	}
	fmt.Println("rendered", outPath)
}

func die(ctx string, err error) {
	if err == nil {
		fmt.Fprintf(os.Stderr, "rendercatalog: %s: unknown error\n", ctx)
	} else {
		fmt.Fprintf(os.Stderr, "rendercatalog: %s: %s\n", ctx, errors.Details(err, nil))
	}
	os.Exit(1)
}
