package uninstall

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
)

// fakeRunner scripts responses keyed by the first argv token, recording the calls made.
type fakeRunner struct {
	resp map[string]struct {
		out  string
		code int
		err  error
	}
	calls [][]string
}

func (f *fakeRunner) run(_ context.Context, argv []string) (string, int, error) {
	f.calls = append(f.calls, argv)
	r := f.resp[argv[0]]
	return r.out, r.code, r.err
}

func nixItem() Item {
	return Item{
		Tool: "nix", Kind: DelegatePathB,
		Delegate: []string{"/nix/nix-installer", "uninstall", "--no-confirm"},
		Verify:   []string{"/usr/sbin/diskutil", "apfs", "list"},
	}
}

// engineB builds an Engine with a fake runner + codesign stub for Path B tests (no real nix).
func engineB(run Runner, codesign func(context.Context, string) error) *Engine {
	return &Engine{Run: run, Codesign: codesign}
}

func okCodesign(context.Context, string) error { return nil }

func TestPathBSuccessVerifiesTeardown(t *testing.T) {
	fr := &fakeRunner{resp: map[string]struct {
		out  string
		code int
		err  error
	}{
		"/nix/nix-installer": {out: "uninstalled", code: 0},
		"/usr/sbin/diskutil": {out: "Container disk1 ... Macintosh HD\n", code: 0}, // no "nix" → clean
	}}
	e := engineB(fr.run, okCodesign)
	res, err := e.applyPathB(context.Background(), nixItem())
	if err != nil {
		t.Fatalf("clean teardown should succeed: %v", err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("expected delegate + probe calls, got %v", fr.calls)
	}
	joined := strings.Join(res.Notes, "; ")
	if !strings.Contains(joined, "teardown verified") {
		t.Fatalf("expected teardown-verified note, got %q", joined)
	}
}

func TestPathBFailClosedOnNonZeroExit(t *testing.T) {
	fr := &fakeRunner{resp: map[string]struct {
		out  string
		code int
		err  error
	}{
		"/nix/nix-installer": {out: "volume busy", code: 1},
	}}
	e := engineB(fr.run, okCodesign)
	_, err := e.applyPathB(context.Background(), nixItem())
	if err == nil {
		t.Fatal("non-zero delegate exit must fail closed")
	}
	if len(fr.calls) != 1 {
		t.Fatalf("must NOT run the probe after a failed delegate, calls=%v", fr.calls)
	}
}

func TestPathBResidueIsIncompleteTeardown(t *testing.T) {
	fr := &fakeRunner{resp: map[string]struct {
		out  string
		code int
		err  error
	}{
		"/nix/nix-installer": {out: "done", code: 0},
		"/usr/sbin/diskutil": {out: "  Nix Store (still mounted)\n", code: 0}, // "nix" present → residue
	}}
	e := engineB(fr.run, okCodesign)
	_, err := e.applyPathB(context.Background(), nixItem())
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("residue after exit 0 must report incomplete teardown, got %v", err)
	}
}

func TestPathBCodesignUnavailableProceeds(t *testing.T) {
	fr := &fakeRunner{resp: map[string]struct {
		out  string
		code int
		err  error
	}{
		"/nix/nix-installer": {out: "done", code: 0},
		"/usr/sbin/diskutil": {out: "clean", code: 0},
	}}
	unavailable := func(context.Context, string) error { return install.ErrCodesignUnavailable }
	e := engineB(fr.run, unavailable)
	res, err := e.applyPathB(context.Background(), nixItem())
	if err != nil {
		t.Fatalf("codesign-unavailable should be noted, not fatal: %v", err)
	}
	if !strings.Contains(strings.Join(res.Notes, "; "), "codesign unavailable") {
		t.Fatalf("expected a codesign-unavailable note, got %v", res.Notes)
	}
}

func TestPathBCodesignTamperIsFatal(t *testing.T) {
	fr := &fakeRunner{resp: map[string]struct {
		out  string
		code int
		err  error
	}{}}
	tampered := func(context.Context, string) error { return errors.New("code object is not signed at all") }
	e := engineB(fr.run, tampered)
	_, err := e.applyPathB(context.Background(), nixItem())
	if err == nil {
		t.Fatal("a real codesign failure on the delegate must be fatal (do not run it)")
	}
	if len(fr.calls) != 0 {
		t.Fatalf("must not run a delegate that failed codesign, calls=%v", fr.calls)
	}
}
