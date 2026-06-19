package cli

import "testing"

func TestDoctorTiers(t *testing.T) {
	tiers := doctorTiers()
	for _, env := range []string{"host", "sandbox", "container", "vm"} {
		row, ok := tiers[env]
		if !ok {
			t.Fatalf("doctorTiers missing %q", env)
		}
		if row["tier"] == "" || row["note"] == "" {
			t.Fatalf("doctorTiers[%q] incomplete: %+v", env, row)
		}
	}
	if tiers["sandbox"]["tier"] != "mistake-guard" {
		t.Fatalf("sandbox tier = %q, want mistake-guard", tiers["sandbox"]["tier"])
	}
}
