package policy

import (
	"fmt"
	"sort"
)

// Resolved is a profile's fully-resolved package set (specs/0058 N0/N2). It is the
// bridge from a profile's declared bundles/packages to the image build and the squid
// allowlist.
type Resolved struct {
	// Packages is the install order: requires before dependents (topological), so
	// node installs before claude-code for every subset. This drives the Dockerfile
	// RUN-step order in IW2.
	Packages []string `json:"packages"`
	// IdentitySet is the same packages sorted + deduped — the order-independent
	// identity that feeds recipeID and image dedup (IW2). Distinct from Packages so
	// identity never depends on declaration order.
	IdentitySet []string `json:"identitySet"`
	// RuntimeEgress is the sorted union of the resolved packages' runtime egress
	// domains, to be UNIONed into the profile's squid allowlist (never relaxes
	// default-deny; specs/0058 N2).
	RuntimeEgress []string `json:"runtimeEgress,omitempty"`
}

// Resolve resolves a profile against the default in-tree catalog.
func Resolve(p Profile) (*Resolved, error) { return DefaultCatalog().Resolve(p) }

// Resolve expands the profile's agent-default bundle + declared bundles + à-la-carte
// packages into the requires-closure, rejecting unknown names, conflicts, and
// requires-cycles, and returns the topological install order, the sorted identity set,
// and the unioned runtime egress (specs/0058 N0). The agent default bundle is always
// included so the agent can launch (the --no-default-bundle opt-out is a later wave).
func (c *Catalog) Resolve(p Profile) (*Resolved, error) {
	// 1. seed names: agent default bundle + declared bundles + à-la-carte packages.
	var seed []string
	if bn := c.DefaultBundle(p.Agent); bn != "" {
		b, ok := c.bndIdx[bn]
		if !ok {
			return nil, fmt.Errorf("resolve: agent %q default bundle %q missing from catalog", p.Agent, bn)
		}
		seed = append(seed, b.Packages...)
	}
	for _, bn := range p.Bundles {
		b, ok := c.bndIdx[bn]
		if !ok {
			return nil, fmt.Errorf("resolve: unknown bundle %q", bn)
		}
		seed = append(seed, b.Packages...)
	}
	seed = append(seed, p.Packages...)

	// 2. requires-closure (BFS; the visited set makes it terminate even on a cycle —
	//    the cycle itself is reported by the topological sort in step 4).
	inSet := make(map[string]bool)
	var queue []string
	add := func(name string) error {
		if inSet[name] {
			return nil
		}
		if _, ok := c.pkgIdx[name]; !ok {
			return fmt.Errorf("resolve: unknown package %q", name)
		}
		inSet[name] = true
		queue = append(queue, name)
		return nil
	}
	for _, s := range seed {
		if err := add(s); err != nil {
			return nil, err
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, r := range c.pkgIdx[queue[i]].Requires {
			if err := add(r); err != nil {
				return nil, err
			}
		}
	}

	names := make([]string, 0, len(inSet))
	for n := range inSet {
		names = append(names, n)
	}

	// 3. conflicts within the closure (asymmetric declarations still caught).
	for _, n := range names {
		for _, x := range c.pkgIdx[n].Conflicts {
			if inSet[x] {
				a, b := n, x
				if a > b {
					a, b = b, a
				}
				return nil, fmt.Errorf("resolve: packages %q and %q conflict", a, b)
			}
		}
	}

	// 4. topological install order (also the cycle check).
	order, err := c.topo(names)
	if err != nil {
		return nil, err
	}

	// 5. identity set: sorted + deduped (order-independent).
	idset := append([]string(nil), names...)
	sort.Strings(idset)

	// 6. runtime egress: sorted union.
	egSeen := make(map[string]bool)
	for _, n := range names {
		for _, d := range c.pkgIdx[n].RuntimeEgress {
			egSeen[d] = true
		}
	}
	var egress []string
	for d := range egSeen {
		egress = append(egress, d)
	}
	sort.Strings(egress)

	return &Resolved{Packages: order, IdentitySet: idset, RuntimeEgress: egress}, nil
}

// topo returns a deterministic topological order of names (a package's Requires come
// before it), or an error naming the packages caught in a requires-cycle. Edges to
// packages outside names are ignored, so it works on any subset (Resolve passes the
// closure; Validate passes every package). Ties break by name for reproducibility.
func (c *Catalog) topo(names []string) ([]string, error) {
	inSet := make(map[string]bool, len(names))
	for _, n := range names {
		inSet[n] = true
	}
	indeg := make(map[string]int, len(names))
	dependents := make(map[string][]string) // r -> packages that require r
	for _, n := range names {
		indeg[n] = 0
	}
	for _, n := range names {
		for _, r := range c.pkgIdx[n].Requires {
			if inSet[r] {
				indeg[n]++
				dependents[r] = append(dependents[r], n)
			}
		}
	}
	var ready []string
	for _, n := range names {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)

	var order []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		grew := false
		for _, m := range dependents[n] {
			indeg[m]--
			if indeg[m] == 0 {
				ready = append(ready, m)
				grew = true
			}
		}
		if grew {
			sort.Strings(ready)
		}
	}
	if len(order) != len(names) {
		done := make(map[string]bool, len(order))
		for _, n := range order {
			done[n] = true
		}
		var stuck []string
		for _, n := range names {
			if !done[n] {
				stuck = append(stuck, n)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("requires cycle among %v", stuck)
	}
	return order, nil
}
