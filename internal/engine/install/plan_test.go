package install

import "testing"

// pin builds a fully-pinned (valid) Pin with a stub sha256/url, for validator + diff tests.
func pin(name, kind, ver string) Pin {
	return Pin{
		Name:    name,
		Kind:    kind,
		Format:  "binary-tarball",
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

func TestPlanClassifiesInstallUpgradeOK(t *testing.T) {
	state := State{
		Toolchains: []Tool{{Name: "mise", Present: true, Version: "mise 2026.6.0 macos-arm64"}},
		Runtimes:   []Tool{{Name: "tart", Present: false}},
	}
	desired := []Pin{
		pin("mise", "toolchain", "2026.6.0"), // present at exact version -> ok
		pin("tart", "runtime", "2.0.0"),      // absent -> install
	}
	res, err := Plan(state, desired)
	if err != nil {
		t.Fatalf("Plan errored: %v", err)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("want 2 actions in manifest order, got %d", len(res.Actions))
	}
	if res.Actions[0].Kind != ActionOK {
		t.Fatalf("mise should be ok, got %s", res.Actions[0].Kind)
	}
	if res.Actions[1].Kind != ActionInstall {
		t.Fatalf("tart should be install, got %s", res.Actions[1].Kind)
	}
	if res.Pending() != 1 {
		t.Fatalf("pending want 1, got %d", res.Pending())
	}
}

func TestPlanDetectsUpgrade(t *testing.T) {
	state := State{Toolchains: []Tool{{Name: "mise", Present: true, Version: "2026.5.0"}}}
	res, err := Plan(state, []Pin{pin("mise", "toolchain", "2026.6.0")})
	if err != nil {
		t.Fatalf("Plan errored: %v", err)
	}
	if res.Actions[0].Kind != ActionUpgrade {
		t.Fatalf("want upgrade, got %s", res.Actions[0].Kind)
	}
	if res.Actions[0].Current != "2026.5.0" {
		t.Fatalf("current want 2026.5.0, got %q", res.Actions[0].Current)
	}
	if res.Actions[0].Desired != "2026.6.0" {
		t.Fatalf("desired want 2026.6.0, got %q", res.Actions[0].Desired)
	}
}

func TestPlanFailsClosedOnBadManifest(t *testing.T) {
	bad := []Pin{{Name: "mise", Kind: "toolchain", Version: "latest", SHA256: "x", URL: "u"}}
	if _, err := Plan(State{}, bad); err == nil {
		t.Fatal("Plan must fail closed on an invalid manifest")
	}
}

func TestValidateDesiredRejectsBadFormat(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Format = "zip"
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("an unknown format must be rejected")
	}
}

func TestValidateDesiredRejectsMissingFormat(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Format = ""
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("an empty format must be rejected (fail-closed)")
	}
}

func TestValidateDesiredRejectsIncompleteSig(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Sig = &Sig{Scheme: "minisign"} // missing pubkey/urls/artifact
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("a partial Sig must be rejected (fail-closed)")
	}
}

func TestPlanCarriesFormatAndSig(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Sig = &Sig{Scheme: "minisign", PubKey: "RWQk", SumsURL: "u", SigURL: "u", Artifact: "a"}
	state := State{Toolchains: []Tool{{Name: "mise", Present: false}}}
	res, err := Plan(state, []Pin{p})
	if err != nil {
		t.Fatalf("Plan errored: %v", err)
	}
	if res.Actions[0].Format != "binary-tarball" {
		t.Fatalf("format not carried onto action: %q", res.Actions[0].Format)
	}
	if res.Actions[0].Sig == nil || res.Actions[0].Sig.Scheme != "minisign" {
		t.Fatalf("sig not carried onto action: %+v", res.Actions[0].Sig)
	}
}

func TestExtractVersion(t *testing.T) {
	cases := map[string]string{
		"mise 2026.6.0 macos-arm64":     "2026.6.0",
		"tart version: 2.0.0 (build 7)": "2.0.0",
		"no version here":               "",
		"v1.2":                          "1.2",
	}
	for in, want := range cases {
		if got := extractVersion(in); got != want {
			t.Errorf("extractVersion(%q) = %q want %q", in, got, want)
		}
	}
}
