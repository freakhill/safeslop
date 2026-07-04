package policy

import (
	"math/rand"
	"strings"
	"testing"
)

func TestHostConsentStatementsMixInvariant(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		got := HostConsentStatements(3, rand.New(rand.NewSource(seed)))
		if len(got) != 3 {
			t.Fatalf("seed %d: got %d statements, want 3", seed, len(got))
		}
		var trues, falses int
		seen := map[string]bool{}
		for _, s := range got {
			if strings.TrimSpace(s.Text) == "" {
				t.Fatalf("seed %d: empty statement text", seed)
			}
			if seen[s.Text] {
				t.Fatalf("seed %d: duplicate statement %q", seed, s.Text)
			}
			seen[s.Text] = true
			if s.Expected {
				trues++
				if s.TierOrigin != "host" {
					t.Errorf("seed %d: true statement %q tier_origin=%q, want host", seed, s.Text, s.TierOrigin)
				}
			} else {
				falses++
				if s.TierOrigin == "host" {
					t.Errorf("seed %d: false decoy %q has tier_origin host", seed, s.Text)
				}
			}
		}
		if trues < 1 || falses < 1 {
			t.Errorf("seed %d: mix invariant broken — %d true, %d false (need >=1 each)", seed, trues, falses)
		}
	}
}

func TestHostConsentStatementsCountFloor(t *testing.T) {
	if got := HostConsentStatements(1, rand.New(rand.NewSource(1))); len(got) < 2 {
		t.Fatalf("count floor not applied: got %d, want >=2", len(got))
	}
}

func TestHostScopeLineHonest(t *testing.T) {
	p := Profile{Environment: "host", Secrets: map[string]string{"GH_TOKEN": "op://x"}}
	line := HostScopeLine(p, []string{"/Volumes/Data", "/Volumes/Backup"})
	for _, want := range []string{"home folder", "2 other mounted volumes", "1 safeslop-injected credential", "full host network"} {
		if !strings.Contains(line, want) {
			t.Errorf("scope line %q missing %q", line, want)
		}
	}
}

func TestHostScopeLineEmptyState(t *testing.T) {
	line := HostScopeLine(Profile{Environment: "host"}, nil)
	for _, want := range []string{"no other mounted volumes", "no safeslop-injected credentials"} {
		if !strings.Contains(line, want) {
			t.Errorf("scope line %q missing %q", line, want)
		}
	}
}

func TestHostHeadlineBodyNamesProfileAndDanger(t *testing.T) {
	body := HostHeadlineBody("risky")
	for _, want := range []string{"risky", "no isolation", "every file", "credentials", "network"} {
		if !strings.Contains(body, want) {
			t.Errorf("headline %q missing %q", body, want)
		}
	}
}
