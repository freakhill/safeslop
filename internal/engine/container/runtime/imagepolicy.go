package runtime

import (
	"encoding/json"
	"strings"
)

// Image-pull policy (specs/0042/0044): the install-time supply chain (pinned engine binary) and the
// run-time supply chain (the images the agent pulls) are DECOUPLED — a verified engine still
// `docker pull ubuntu:latest`-es an unsigned, mutable image. So agent pulls are forced to immutable
// sha256 digests and a cosign/sigstore policy.json (mounted in the guest) rejects unsigned images at
// pull time.

// rejectsMutableTag reports whether an image reference is a mutable tag (so it must be rejected). A
// reference is acceptable ONLY if it pins an immutable digest (`@sha256:...`); a bare tag (including
// `:latest`, an implicit tag, or any `:tag`) is mutable and rejected.
func rejectsMutableTag(ref string) bool {
	at := strings.LastIndex(ref, "@sha256:")
	return at < 0 // no digest => mutable => reject
}

// RewriteOrReject passes an image reference through only if it is digest-pinned; otherwise it returns a
// non-nil error so the agent cannot pull a mutable tag.
func RewriteOrReject(ref string) (string, error) {
	if rejectsMutableTag(ref) {
		return "", &mutableTagError{ref: ref}
	}
	return ref, nil
}

type mutableTagError struct{ ref string }

func (e *mutableTagError) Error() string {
	return "image pull rejected: " + e.ref + " is not pinned to an immutable @sha256: digest (mutable tags like :latest are refused; specs/0044)"
}

// cosignPolicyJSON renders a containers signature policy.json: default REJECT, with sigstore/cosign
// keyless verification required for the allowed registries. Staged into the guest so nerdctl/containerd
// refuse unsigned images at pull time. A reject-by-default policy is the fail-closed posture.
func cosignPolicyJSON(allowedRegistries []string) ([]byte, error) {
	type req struct {
		Type    string         `json:"type"`
		Keyless map[string]any `json:"keyless,omitempty"`
	}
	transports := map[string]map[string][]req{
		"docker": {},
	}
	for _, r := range allowedRegistries {
		transports["docker"][r] = []req{{Type: "sigstoreSigned", Keyless: map[string]any{"fulcioCAData": "", "rekorPublicKeyData": ""}}}
	}
	policy := map[string]any{
		"default":    []req{{Type: "reject"}},
		"transports": transports,
	}
	return json.MarshalIndent(policy, "", "  ")
}
