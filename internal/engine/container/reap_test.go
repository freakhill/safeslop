package container

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
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

func TestReapByInvocationUsesOnlyExactRandomOwnershipLabel(t *testing.T) {
	const id = "run-0123456789abcdef0123456789abcdef"
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.invocation=" + id:        "ctr-direct\n",
		"network ls -q --filter label=safeslop.invocation=" + id: "net-direct\n",
	})
	if err := ReapByInvocation(context.Background(), eng, id); err != nil {
		t.Fatalf("ReapByInvocation: %v", err)
	}
	eng.assertRan(t, "rm -f ctr-direct")
	eng.assertRan(t, "network rm net-direct")
	if err := ReapByInvocation(context.Background(), eng, "profile-name"); err == nil {
		t.Fatal("profile name accepted as direct cleanup authority")
	}
}

func TestSweepDeadInvocationsKeepsLiveAndMalformedMarkers(t *testing.T) {
	root := t.TempDir()
	const dead = "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const live = "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const malformed = "run-cccccccccccccccccccccccccccccccc"
	for _, id := range []string{dead, live, malformed} {
		if err := os.Mkdir(filepath.Join(root, id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteInvocationMarker(filepath.Join(root, dead), dead, os.Getpid(), "definitely-not-the-current-process-token"); err != nil {
		t.Fatal(err)
	}
	liveToken, _ := engsession.ProcessStartToken(os.Getpid())
	if err := WriteInvocationMarker(filepath.Join(root, live), live, os.Getpid(), liveToken); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, malformed, ".safeslop-stage"), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.invocation=" + dead:        "ctr-dead\n",
		"network ls -q --filter label=safeslop.invocation=" + dead: "net-dead\n",
	})
	if err := SweepDeadInvocations(context.Background(), eng, root); err != nil {
		t.Fatalf("SweepDeadInvocations: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, dead)); !os.IsNotExist(err) {
		t.Fatalf("dead invocation stage remains: %v", err)
	}
	for _, id := range []string{live, malformed} {
		if _, err := os.Stat(filepath.Join(root, id)); err != nil {
			t.Fatalf("unproven invocation %s was removed: %v", id, err)
		}
	}
	eng.assertRan(t, "rm -f ctr-dead")
	eng.assertRan(t, "network rm net-dead")
	eng.assertNotRan(t, "label=safeslop.invocation="+live)
}

func TestInvocationMarkerIsValueFree(t *testing.T) {
	const id = "run-dddddddddddddddddddddddddddddddd"
	stage := filepath.Join(t.TempDir(), id)
	if err := WriteInvocationMarker(stage, id, os.Getpid(), "process-token"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(stage, ".safeslop-stage"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields["invocation_id"] != id || fields["pid"] == nil || fields["process_token"] == nil {
		t.Fatalf("marker fields = %#v", fields)
	}
	for _, forbidden := range []string{"workspace", "credential", "secret", "profile"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("marker contains forbidden field %q: %s", forbidden, body)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "secrets.env"), []byte("SECRET_CANARY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RetainInvocationMarker(stage); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".safeslop-stage" {
		t.Fatalf("marker retention kept staged bearer files: %v", entries)
	}
}

func TestMaterializeRunPreservesInvocationMarker(t *testing.T) {
	const id = "run-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	stage := filepath.Join(t.TempDir(), id)
	if err := WriteInvocationMarker(stage, id, os.Getpid(), "process-token"); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(stage, ".safeslop-stage")
	before, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := materializeRun(composeParams{RuntimeDir: stage, StageDir: stage, Workspace: t.TempDir(), InvocationID: id, AgentImage: "local/test:fixture"}, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("materializeRun replaced invocation marker:\nbefore=%s\nafter=%s", before, after)
	}
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

func TestLiveSessionsProtectsLegacyHashSuffixedOwnershipLabel(t *testing.T) {
	dir := t.TempDir()
	const id = "sess-live-legacy"
	const workspace = "/tmp/legacy-workspace"
	writeFile(t, filepath.Join(dir, id+".json"), `{
  "session_id": "sess-live-legacy",
  "agent": "fish",
  "workspace": "/tmp/legacy-workspace",
  "environment": "container",
  "network": "deny",
  "status": "running",
  "created_at": "2026-06-26T00:00:00Z",
  "updated_at": "2026-06-26T00:00:00Z"
}
`)
	live, err := LiveSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	legacyLabel := LegacySessionReapLabel(id, workspace)
	if !live[id] || !live[legacyLabel] {
		t.Fatalf("live labels = %#v, want bare %q and deployed %q", live, id, legacyLabel)
	}
}

func TestSweepManagedOrphansDoesNotReapLiveLegacyLayout(t *testing.T) {
	dir := t.TempDir()
	const id = "sess-live-legacy"
	const workspace = "/tmp/legacy-workspace"
	writeFile(t, filepath.Join(dir, id+".json"), `{
  "session_id": "sess-live-legacy",
  "agent": "fish",
  "workspace": "/tmp/legacy-workspace",
  "environment": "container",
  "network": "deny",
  "status": "running",
  "created_at": "2026-06-26T00:00:00Z",
  "updated_at": "2026-06-26T00:00:00Z"
}
`)
	label := LegacySessionReapLabel(id, workspace)
	eng := newFakeEngine(t, map[string]string{
		"ps -aq --filter label=safeslop.managed=true":                         "ctr-live\n",
		"inspect -f {{ index .Config.Labels \"safeslop.session\" }} ctr-live": label + "\n",
		"network ls -q --filter label=safeslop.managed=true":                  "\n",
	})
	live, err := LiveSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SweepManagedOrphans(context.Background(), eng, live); err != nil {
		t.Fatal(err)
	}
	eng.assertNotRan(t, "rm -f ctr-live")
	eng.assertNotRan(t, "ps -aq --filter label=safeslop.session="+label)
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
