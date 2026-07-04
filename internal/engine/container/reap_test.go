package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestReapBySessionRemovesContainersAndNetworksByLabel(t *testing.T) {
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.session=sess-dead":        "ctr-one\nctr-two\n",
		"network ls -q --filter label=safeslop.session=sess-dead": "net-one\nnet-two\n",
	})

	if err := ReapBySession(context.Background(), eng, "sess-dead"); err != nil {
		t.Fatalf("reap: %v", err)
	}

	eng.assertRan(t, "rm -f ctr-one ctr-two")
	eng.assertRan(t, "network rm net-one net-two")
}

func TestReapBySessionNoopsWhenNothingMatches(t *testing.T) {
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.session=sess-empty":        "\n",
		"network ls -q --filter label=safeslop.session=sess-empty": "\n",
	})

	if err := ReapBySession(context.Background(), eng, "sess-empty"); err != nil {
		t.Fatalf("reap empty: %v", err)
	}

	eng.assertNotRan(t, "rm -f")
	eng.assertNotRan(t, "network rm")
}

func TestSweepManagedOrphansSkipsLiveSessions(t *testing.T) {
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.managed=true":                               "ctr-live\nctr-dead\nctr-nolabel\n",
		"inspect -f {{ index .Config.Labels \"safeslop.session\" }} ctr-live":       "sess-live\n",
		"inspect -f {{ index .Config.Labels \"safeslop.session\" }} ctr-dead":       "sess-dead\n",
		"inspect -f {{ index .Config.Labels \"safeslop.session\" }} ctr-nolabel":    "<no value>\n",
		"network ls -q --filter label=safeslop.managed=true":                        "net-live\nnet-dead-only\nnet-nolabel\n",
		"network inspect -f {{ index .Labels \"safeslop.session\" }} net-live":      "sess-live\n",
		"network inspect -f {{ index .Labels \"safeslop.session\" }} net-dead-only": "sess-dead2\n",
		"network inspect -f {{ index .Labels \"safeslop.session\" }} net-nolabel":   "<no value>\n",
		"ps -aq --filter label=safeslop.session=sess-dead":                          "ctr-dead\n",
		"network ls -q --filter label=safeslop.session=sess-dead":                   "net-dead\n",
		"ps -aq --filter label=safeslop.session=sess-dead2":                         "\n",
		"network ls -q --filter label=safeslop.session=sess-dead2":                  "net-dead-only\n",
	})

	if err := SweepManagedOrphans(context.Background(), eng, map[string]bool{"sess-live": true}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	eng.assertRan(t, "rm -f ctr-dead")
	eng.assertRan(t, "network rm net-dead")
	eng.assertRan(t, "network rm net-dead-only")
	eng.assertNotRan(t, "rm -f ctr-live")
}

func TestGCImagesKeepsProfileLockAndLiveSessionReferences(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "safeslop.cue"), `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {agent: "claude", environment: "container", network: "deny"}
	}
}
`)
	writeFile(t, filepath.Join(root, "safeslop.lock.json"), `{"recipeID":"locked123456","agent":"pi","base":"debian","packages":["node","pi"],"versions":{"node":"22.16.0","pi":"0.10.0"}}
`)
	storeDir := filepath.Join(t.TempDir(), "sessions")
	writeFile(t, filepath.Join(storeDir, "sess-live.json"), `{
  "session_id": "sess-live",
  "agent": "pi",
  "workspace": "/tmp/ws",
  "environment": "container",
  "network": "deny",
  "recipeID": "live12345678",
  "image": "local/safeslop-tools:live12345678",
  "status": "running",
  "created_at": "2026-06-26T00:00:00Z",
  "updated_at": "2026-06-26T00:00:00Z"
}
`)

	resolved, err := policy.Resolve(policy.Profile{Agent: "claude", Environment: "container", Network: "deny"})
	if err != nil {
		t.Fatalf("resolve profile fixture: %v", err)
	}
	profileRecipe, err := ResolveRecipe(resolved.IdentitySet)
	if err != nil {
		t.Fatalf("resolve profile recipe: %v", err)
	}

	eng := newFakeEngine(t, map[string]string{
		"image ls --format {{.Repository}}:{{.Tag}} {{.CreatedAt}} --filter label=safeslop.managed=true": strings.Join([]string{
			profileRecipe.AgentImage + " 2026-06-30 12:00:00 +0000 UTC",
			"local/safeslop-tools:locked123456 2026-06-30 11:00:00 +0000 UTC",
			"local/safeslop-tools:live12345678 2026-06-30 10:00:00 +0000 UTC",
			"local/safeslop-tools:old11111111 2026-06-30 09:00:00 +0000 UTC",
		}, "\n") + "\n",
	})

	protected := GCProtection{PolicyPaths: []string{filepath.Join(root, "safeslop.cue")}, LockPaths: []string{filepath.Join(root, "safeslop.lock.json")}, SessionDir: storeDir}
	removed, err := GCImages(context.Background(), eng, GCOptions{Keep: 0}, protected)
	if err != nil {
		t.Fatalf("gc images: %v", err)
	}
	if strings.Join(removed, ",") != "local/safeslop-tools:old11111111" {
		t.Fatalf("removed = %v, want only old image", removed)
	}
	eng.assertRan(t, "image rm local/safeslop-tools:old11111111")
	for _, protected := range []string{profileRecipe.AgentImage, "local/safeslop-tools:locked123456", "local/safeslop-tools:live12345678"} {
		eng.assertNotRan(t, "image rm "+protected)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
