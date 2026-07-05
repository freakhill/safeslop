package container

import (
	"context"
	"testing"
)

// TestReapManagedRemovesAllManagedContainersAndNetworks pins the teardown that `safeslop down`
// now relies on solely (specs/0074 Bug 2): a label sweep of every safeslop.managed container and
// network, independent of any compose project. Uses the shared fakeEngine seam.
func TestReapManagedRemovesAllManagedContainersAndNetworks(t *testing.T) {
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.managed=true":        "ctr-a\nctr-b\n",
		"network ls -q --filter label=safeslop.managed=true": "net-a\n",
	})

	if err := ReapManaged(context.Background(), eng); err != nil {
		t.Fatalf("reap managed: %v", err)
	}

	eng.assertRan(t, "rm -f ctr-a ctr-b")
	eng.assertRan(t, "network rm net-a")
}
