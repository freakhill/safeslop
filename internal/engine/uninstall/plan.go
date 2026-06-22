// Package uninstall is the receipt-driven, fail-closed, consent-gated mirror of the install arc
// (specs/0040/0041). It removes ONLY what the install receipt says safeslop placed: Path A artifacts it
// owns (own-and-remove, hash-verified, recoverable from a trash dir) and Path B system state it
// delegates to the tool's own designated uninstaller (verify the teardown, honest that it is
// irreversible). It never reconstructs intent from install.DesiredState(), and never touches a tool it
// did not install ("never remove what you didn't install").
package uninstall

import (
	"sort"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/receipt"
)

// Kind is the removal discipline for one item.
type Kind int

const (
	RemovePathA   Kind = iota // safeslop placed the files — own-and-remove, hash-verified, trash-recoverable
	DelegatePathB             // a verified installer placed system state — delegate to its own uninstaller
)

func (k Kind) String() string {
	if k == DelegatePathB {
		return "B"
	}
	return "A"
}

// Item is one tool to remove, materialised from its receipt entry.
type Item struct {
	Tool         string
	Kind         Kind
	Version      string
	Reversible   bool           // Path A (trash-recoverable) is true; Path B (destroyed volume/daemon) is false
	SelfUpdating bool           // Path A: tolerate the expected on-disk hash drift (e.g. claude)
	Files        []receipt.File // Path A: artifacts to remove
	Delegate     []string       // Path B: the tool's own uninstaller argv
	Verify       []string       // Path B: post-teardown residue probe argv (optional)
}

// Untouched is a tool present on the box that uninstall will NOT remove, with the reason — surfaced in
// the plan so the user never falsely believes the machine is fully clean (Docker, hand-installed tools).
type Untouched struct {
	Tool   string
	Path   string
	Reason string
}

// Plan is what `uninstall apply` would do: the receipted items to remove plus the untouched tools.
type Plan struct {
	Items     []Item
	Untouched []Untouched
}

// Reversible reports whether every item can be rolled back (all Path A). The consent copy uses this to
// state the asymmetry honestly rather than implying symmetric recoverability.
func (p Plan) Reversible() bool {
	for _, it := range p.Items {
		if !it.Reversible {
			return false
		}
	}
	return true
}

// HasIrreversible reports whether any item is a Path B (irreversible) removal.
func (p Plan) HasIrreversible() bool { return !p.Reversible() }

// Build assembles a removal plan from the receipt store and a live install.State. When tools is empty,
// every receipted tool is targeted; otherwise only the named ones. A named tool with no receipt is NOT
// removed — it is reported as Untouched ("no safeslop receipt"), upholding "never remove what you didn't
// install". The Untouched list also includes every probed-present tool with no receipt (Docker etc.) and
// every tool the receipt explicitly recorded as unmanaged.
func Build(store *receipt.Store, st install.State, tools []string) (Plan, error) {
	want := map[string]bool{}
	for _, t := range tools {
		want[t] = true
	}

	var p Plan
	receipted := map[string]bool{}
	for _, e := range store.All() {
		receipted[e.Tool] = true
		if len(want) > 0 && !want[e.Tool] {
			continue
		}
		p.Items = append(p.Items, itemFromEntry(e))
	}

	// A named tool we have no receipt for can't be removed — surface it, don't guess.
	for _, t := range tools {
		if !receipted[t] {
			p.Untouched = append(p.Untouched, Untouched{Tool: t, Reason: "no safeslop receipt — not installed by safeslop"})
		}
	}

	// Enumerate present-but-unreceipted tools from the live probe (Docker, hand-installed) so the plan is
	// honest about what it leaves behind. Skipped when the caller named specific tools (the untouched set
	// is then only the named-but-unreceipted ones above).
	if len(want) == 0 {
		for _, t := range allProbed(st) {
			if t.Present && !receipted[t.Name] {
				p.Untouched = append(p.Untouched, Untouched{Tool: t.Name, Path: t.Path, Reason: "not installed by safeslop"})
			}
		}
		for tool, path := range store.Unmanaged() {
			if !receipted[tool] {
				p.Untouched = append(p.Untouched, Untouched{Tool: tool, Path: path, Reason: "recorded as unmanaged at install time"})
			}
		}
	}

	sort.Slice(p.Untouched, func(i, j int) bool { return p.Untouched[i].Tool < p.Untouched[j].Tool })
	return p, nil
}

func itemFromEntry(e receipt.Entry) Item {
	it := Item{
		Tool:         e.Tool,
		Version:      e.Version,
		SelfUpdating: e.SelfUpdating,
		Files:        e.Files,
		Delegate:     e.Uninstall,
		Verify:       e.UninstallVerify,
	}
	if e.Path == "B" {
		it.Kind = DelegatePathB
		it.Reversible = false
	} else {
		it.Kind = RemovePathA
		it.Reversible = true
	}
	return it
}

func allProbed(st install.State) []install.Tool {
	out := make([]install.Tool, 0, len(st.Toolchains)+len(st.Runtimes))
	out = append(out, st.Toolchains...)
	out = append(out, st.Runtimes...)
	return out
}
