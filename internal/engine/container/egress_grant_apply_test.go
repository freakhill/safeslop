package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const proxyGenerationInspectFormat = `{{ index .Config.Labels "safeslop.egress-revision" }} {{ index .Config.Labels "safeslop.egress-hash" }}`

func egressGenerationFixture(t *testing.T, revision int, grants []SessionGrant) (string, string, string, EgressGeneration, *fakeEngine) {
	t.Helper()
	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {proxy: {image: fixture}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exercise upgrade bootstrap from the deployed individual-file include.
	if err := os.WriteFile(filepath.Join(dir, "squid.conf"), []byte("include /etc/squid/session-grants.conf\nhttp_access deny all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generation, _, err := BuildEgressGeneration(grants, revision)
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(dir, "egress.override.yml")
	eng := newFakeEngine(t, map[string]string{})
	eng.outputs["ps -q --filter label=com.docker.compose.project="+filepath.Base(dir)+" --filter label=com.docker.compose.service=proxy"] = "proxy-id\n"
	eng.outputs[composeCommandKeyWithOverrides(t, composeFile, []string{override}, "ps", "--status", "running", "-q", "proxy")] = "proxy-id-full\n"
	eng.outputs["inspect -f "+proxyGenerationInspectFormat+" proxy-id-full"] = string(rune('0'+revision)) + " " + generation.Hash + "\n"
	eng.outputs[composeCommandKeyWithOverrides(t, composeFile, []string{override}, "exec", "-T", "proxy", "sha256sum", "/etc/squid/safeslop.d/session-grants.conf")] = generation.Hash + "  /etc/squid/safeslop.d/session-grants.conf\n"
	return dir, composeFile, override, generation, eng
}

func TestBuildEgressGenerationBindsRevisionAndExactRules(t *testing.T) {
	grants := []SessionGrant{{Host: "example.com", Port: 443}}
	first, body, err := BuildEgressGeneration(grants, 7)
	if err != nil {
		t.Fatal(err)
	}
	again, againBody, err := BuildEgressGeneration(grants, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first != again || string(body) != string(againBody) || len(first.Hash) != 64 {
		t.Fatalf("generation is not deterministic: first=%+v again=%+v", first, again)
	}
	if !strings.Contains(string(body), "# safeslop-egress-revision: 7") || !strings.Contains(string(body), `^example\.com$`) {
		t.Fatalf("generation bytes lack revision/exact grant:\n%s", body)
	}
	next, _, err := BuildEgressGeneration(grants, 8)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hash == first.Hash {
		t.Fatal("distinct durable revisions produced the same generation hash")
	}
}

func TestApplyEgressGenerationReplacesAndAcknowledgesProxy(t *testing.T) {
	grants := []SessionGrant{{Host: "example.com", Port: 443}}
	dir, composeFile, override, wanted, eng := egressGenerationFixture(t, 1, grants)

	got, err := ApplyEgressGeneration(context.Background(), eng, composeFile, dir, grants, 1)
	if err != nil {
		t.Fatalf("ApplyEgressGeneration: %v", err)
	}
	if got != wanted {
		t.Fatalf("generation = %+v, want %+v", got, wanted)
	}
	eng.assertRan(t, composeCommandKeyWithOverrides(t, composeFile, []string{override}, "up", "-d", "--no-deps", "--force-recreate", "proxy"))
	eng.assertRan(t, composeCommandKeyWithOverrides(t, composeFile, []string{override}, "exec", "-T", "proxy", "bash", "-ec", proxyReadyCommand))
	body, err := os.ReadFile(filepath.Join(dir, "proxy-overlay", "session-grants.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), wanted.Hash[:0]+`^example\.com$`) {
		t.Fatalf("candidate overlay missing exact grant: %s", body)
	}
	squid, err := os.ReadFile(filepath.Join(dir, "squid.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(squid), "/etc/squid/safeslop.d/session-grants.conf") || strings.Contains(string(squid), "include /etc/squid/session-grants.conf") {
		t.Fatalf("legacy Squid include was not upgraded atomically: %s", squid)
	}
	overrideBody, err := os.ReadFile(override)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"safeslop.egress-revision", wanted.Hash, "create_host_path: false", "/etc/squid/safeslop.d"} {
		if !strings.Contains(string(overrideBody), want) {
			t.Fatalf("override missing %q:\n%s", want, overrideBody)
		}
	}
}

func TestEnsureEgressGenerationSkipsReplacementOnPositiveAck(t *testing.T) {
	grants := []SessionGrant{{Host: "example.com", Port: 443}}
	dir, composeFile, _, wanted, eng := egressGenerationFixture(t, 2, grants)
	// Inspection without an override is how a later command observes the running proxy.
	eng.outputs[composeCommandKey(t, composeFile, "ps", "--status", "running", "-q", "proxy")] = "proxy-id-full\n"
	eng.outputs[composeCommandKey(t, composeFile, "exec", "-T", "proxy", "sha256sum", "/etc/squid/safeslop.d/session-grants.conf")] = wanted.Hash + "  file\n"

	got, err := EnsureEgressGeneration(context.Background(), eng, composeFile, dir, grants, 2)
	if err != nil {
		t.Fatalf("EnsureEgressGeneration: %v", err)
	}
	if got != wanted {
		t.Fatalf("generation = %+v, want %+v", got, wanted)
	}
	eng.assertNotRan(t, "compose -p "+filepath.Base(dir)+" --project-directory "+dir+" -f "+composeFile+" -f ")
}

func TestInspectEgressGenerationRejectsAnOrphanedSecondProxy(t *testing.T) {
	dir, composeFile, _, wanted, eng := egressGenerationFixture(t, 2, []SessionGrant{{Host: "example.com", Port: 443}})
	eng.outputs[composeCommandKey(t, composeFile, "ps", "--status", "running", "-q", "proxy")] = "proxy-id-full\n"
	eng.outputs[composeCommandKey(t, composeFile, "exec", "-T", "proxy", "sha256sum", "/etc/squid/safeslop.d/session-grants.conf")] = wanted.Hash + "  file\n"
	eng.outputs["ps -q --filter label=com.docker.compose.project="+filepath.Base(dir)+" --filter label=com.docker.compose.service=proxy"] = "proxy-id\norphan-id\n"

	if _, err := InspectEgressGeneration(context.Background(), eng, composeFile); !errors.Is(err, ErrEgressGenerationUncertain) {
		t.Fatalf("InspectEgressGeneration error = %v, want uncertainty for two proxies", err)
	}
}

func TestApplyEgressGenerationHashMismatchIsUncertain(t *testing.T) {
	grants := []SessionGrant{{Host: "example.com", Port: 443}}
	dir, composeFile, override, _, eng := egressGenerationFixture(t, 3, grants)
	eng.outputs[composeCommandKeyWithOverrides(t, composeFile, []string{override}, "exec", "-T", "proxy", "sha256sum", "/etc/squid/safeslop.d/session-grants.conf")] = strings.Repeat("0", 64) + "  file\n"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := ApplyEgressGeneration(ctx, eng, composeFile, dir, grants, 3); !errors.Is(err, ErrEgressGenerationUncertain) {
		t.Fatalf("ApplyEgressGeneration error = %v, want ErrEgressGenerationUncertain", err)
	}
}

func TestApplyEgressGenerationWriteFailureDoesNotReplaceProxy(t *testing.T) {
	grants := []SessionGrant{{Host: "example.com", Port: 443}}
	dir, composeFile, override, _, eng := egressGenerationFixture(t, 4, grants)
	old := []byte("old-complete-overlay\n")
	if err := os.MkdirAll(filepath.Join(dir, "proxy-overlay"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "proxy-overlay", "session-grants.conf")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ApplyEgressGeneration(context.Background(), eng, composeFile, dir, grants, 4, WithOverlayTestHook(func(string) error {
		return os.ErrPermission
	}))
	if err == nil || !strings.Contains(err.Error(), "write session grants overlay") {
		t.Fatalf("ApplyEgressGeneration error = %v, want write failure", err)
	}
	eng.assertNotRan(t, composeCommandKeyWithOverrides(t, composeFile, []string{override}, "up", "-d", "--no-deps", "--force-recreate", "proxy"))
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != string(old) {
		t.Fatalf("write failure changed prior overlay: %q err=%v", after, readErr)
	}
}
