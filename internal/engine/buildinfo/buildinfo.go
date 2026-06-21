// Package buildinfo carries build-stamped facts about the running safeslop binary that the UI must not
// overstate. The cockpit's install precautions lean on "a checksum compiled into the notarized safeslop
// binary" as a root of trust — but that claim only holds for the Developer-ID-signed + notarized release
// build. A dev/adhoc build has no Apple signature sealing its embedded pin set, so it must not make the
// notarization claim. Release records which build this is so the precaution wording stays honest
// (specs/0036 honesty fix).
package buildinfo

// Release is flipped to "true" via -ldflags by the notarized release build only (`make sign`, which
// rebuilds dist with RELEASE=1). It stays "false" for `make build` dev binaries and adhoc builds. Kept a
// string so it can be set with the linker's -X flag; read it through Notarized.
var Release = "false"

// Notarized reports whether this build is the Developer-ID-signed + notarized release artifact, i.e.
// whether the embedded pin set is actually sealed under an Apple code signature.
func Notarized() bool { return Release == "true" }
