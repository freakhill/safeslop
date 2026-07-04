package policy

import (
	"fmt"
	"os"
	"testing"
)

func TestRepositoryHasNoLatestPins(t *testing.T) {
	if os.Getenv("SAFESLOP_SKIP_REPO_PINNING_GATE") == "1" {
		t.Skip("repo pinning gate disabled by environment")
	}
	findings, err := CheckNoLatestPins("../../..")
	if err != nil {
		t.Fatalf("pinning scan: %v", err)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintf(os.Stderr, "%s:%d: forbidden %s in %s\n", f.Path, f.Line, f.Pattern, f.Text)
		}
		t.Fatalf("found %d unpinned latest reference(s)", len(findings))
	}
}
