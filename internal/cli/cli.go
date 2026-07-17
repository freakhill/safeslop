// Package cli is the safeslop command tree. Every command drives the engine
// packages and (with --json) emits machine-readable output so a future GUI can
// drive the same engine without re-implementing logic (specs/0001 §6, §A).
package cli

import (
	"errors"
	"fmt"
	"os"
)

// Version is overridden at build time via -ldflags "-X .../cli.Version=...".
var Version = "dev"

// Execute runs the root command and exits non-zero on error.
func Execute() {
	d := defaultDependencies()
	if err := newRootWithDeps(d).Execute(); err != nil {
		if !errors.Is(err, errOutputEmitted) {
			if !d.jsonOut {
				fmt.Fprintln(os.Stderr, "safeslop:", err)
			} else {
				emitJSON(map[string]any{"ok": false, "error": err.Error()})
			}
		}
		os.Exit(1)
	}
}
