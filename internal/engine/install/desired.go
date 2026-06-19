package install

// DesiredState is the embedded, pinned + checksummed install manifest for darwin-arm64 — the only
// platform SafeSlop targets (specs/0012). It is the desired-state half of `install plan`; apply
// (SP7b-3) consumes URL + SHA256. Bump entries as data edits; TestDesiredStateIsFailClosed +
// ValidateDesired guarantee every entry stays fully pinned. Tools probed by Status but absent here
// (docker, nix) are not yet installer-managed — their multi-component installers are a later slice.
//
// Seeded empty; Task 5 of specs/0020 populates the real mise + tart darwin-arm64 artifacts.
func DesiredState() []Pin {
	return nil
}
