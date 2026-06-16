// Command slop is the single-binary entry point for the slop engine.
//
// It is intentionally thin: it only wires the cobra command tree to the engine
// packages under internal/engine (specs/0001 §6 — engine library + thin CLI).
package main

import "github.com/freakhill/agentic_tactical_boots/internal/cli"

func main() {
	cli.Execute()
}
