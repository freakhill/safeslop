// Command safeslop is the single-binary entry point for the safeslop engine.
//
// It is intentionally thin: it only wires the cobra command tree to the engine
// packages under internal/engine (specs/0001 §6 — engine library + thin CLI).
package main

import "github.com/freakhill/safeslop/internal/cli"

func main() {
	cli.Execute()
}
