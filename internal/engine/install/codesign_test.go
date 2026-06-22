package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyCodesign(t *testing.T) {
	if _, err := os.Stat(codesignPath); err != nil {
		t.Skipf("codesign unavailable (%s) — not macOS", codesignPath)
	}
	ctx := context.Background()

	// A genuinely signed Apple system binary must verify.
	if err := VerifyCodesign(ctx, "/bin/ls"); err != nil {
		t.Fatalf("VerifyCodesign(/bin/ls) should pass on macOS, got %v", err)
	}

	// A junk file is not validly signed and must fail (not error out as unavailable).
	junk := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(junk, []byte("not a mach-o"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCodesign(ctx, junk); err == nil {
		t.Fatal("VerifyCodesign on an unsigned junk file should fail")
	} else if err == ErrCodesignUnavailable {
		t.Fatalf("junk file should fail verification, not report codesign unavailable")
	}
}
