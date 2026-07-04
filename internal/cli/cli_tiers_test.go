package cli

import "testing"

func TestDoctorTiers(t *testing.T) {
	tiers := doctorTiers()
	for _, env := range []string{"host", "container"} {
		row, ok := tiers[env]
		if !ok {
			t.Fatalf("doctorTiers missing %q", env)
		}
		if row["tier"] == "" || row["note"] == "" {
			t.Fatalf("doctorTiers[%q] incomplete: %+v", env, row)
		}
	}
	// The removed sandbox and vm tiers must not reappear (specs/0053).
	if _, ok := tiers["sandbox"]; ok {
		t.Fatalf("doctorTiers still lists the removed sandbox tier: %+v", tiers)
	}
	if _, ok := tiers["vm"]; ok {
		t.Fatalf("doctorTiers still lists the removed vm tier: %+v", tiers)
	}
	if tiers["container"]["tier"] != "egress-allowlisted" {
		t.Fatalf("container tier = %q, want egress-allowlisted", tiers["container"]["tier"])
	}
}
