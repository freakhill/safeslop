package install

import "testing"

// pin builds a fully-pinned (valid) Pin with a stub sha256/url, for validator + diff tests.
func pin(name, kind, ver string) Pin {
	return Pin{
		Name:    name,
		Kind:    kind,
		Version: ver,
		SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		URL:     "https://example.test/" + name,
	}
}

func TestValidateDesiredAcceptsFullyPinned(t *testing.T) {
	if err := ValidateDesired([]Pin{pin("mise", "toolchain", "2026.6.0")}); err != nil {
		t.Fatalf("fully pinned manifest should validate: %v", err)
	}
}

func TestValidateDesiredEmptyOK(t *testing.T) {
	if err := ValidateDesired(nil); err != nil {
		t.Fatalf("empty manifest should validate vacuously: %v", err)
	}
}

func TestValidateDesiredRejectsLatest(t *testing.T) {
	if err := ValidateDesired([]Pin{pin("mise", "toolchain", "latest")}); err == nil {
		t.Fatal("a 'latest' version must be rejected (fail-closed)")
	}
}

func TestValidateDesiredRejectsMissingSHA(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.SHA256 = ""
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("a missing sha256 must be rejected (fail-closed)")
	}
}

func TestValidateDesiredRejectsShortSHA(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.SHA256 = "abc123"
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("a non-64-hex sha256 must be rejected")
	}
}

func TestValidateDesiredRejectsBadKind(t *testing.T) {
	if err := ValidateDesired([]Pin{pin("mise", "wat", "2026.6.0")}); err == nil {
		t.Fatal("an invalid kind must be rejected")
	}
}

func TestValidateDesiredRejectsDuplicate(t *testing.T) {
	ps := []Pin{pin("mise", "toolchain", "2026.6.0"), pin("mise", "toolchain", "2026.7.0")}
	if err := ValidateDesired(ps); err == nil {
		t.Fatal("duplicate tool names must be rejected")
	}
}
