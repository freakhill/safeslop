package policy

import (
	"fmt"
	"math/rand"
)

// ConsentStatement is one comprehension row for the host-launch gate (specs/0030). The engine authors
// every sentence and sets every Expected, so the affirmed text can never drift from what actually runs
// (single source of truth). Expected is the ground truth — is this statement TRUE of this host run?
// TierOrigin records which tier the sentence actually describes: "host" for the true rows, and
// "container" for the false cross-tier decoys the user must reject.
type ConsentStatement struct {
	Text       string
	Expected   bool
	TierOrigin string
}

// HostHeadlineBody is the fixed honesty-anchor paragraph shown above the comprehension rows for a host
// launch. It is always true and never an answerable row — it states the unconditional host blast radius
// so the consequence is on screen regardless of which decoys get drawn.
func HostHeadlineBody(name string) string {
	return fmt.Sprintf("This agent runs on your Mac as you — no isolation. It can read and write every "+
		"file your account can, use your logged-in credentials, and reach any network your Mac can "+
		"reach. Nothing about profile %q is sandboxed.", name)
}

// HostScopeLine is the per-launch-distinct line the engine derives from live machine state, so the READ
// card varies run to run and offers no fixed text to memorise (the anti-habituation anchor). volumes are
// the non-boot volumes mounted right now; the credential count is the number of credentials THIS profile
// additionally injects — the ambient logged-in credentials are already covered by the headline, so this
// stays honest about what safeslop itself stages and never claims to enumerate the host keychain.
func HostScopeLine(p Profile, volumes []string) string {
	vol := "no other mounted volumes"
	if len(volumes) == 1 {
		vol = "1 other mounted volume"
	} else if len(volumes) > 1 {
		vol = fmt.Sprintf("%d other mounted volumes", len(volumes))
	}
	injected := len(p.Secrets) + len(credLines(p.Credentials))
	cred := "no safeslop-injected credentials"
	if injected == 1 {
		cred = "1 safeslop-injected credential"
	} else if injected > 1 {
		cred = fmt.Sprintf("%d safeslop-injected credentials", injected)
	}
	return fmt.Sprintf("This run: your home folder + %s, %s, full host network.", vol, cred)
}

// hostTrueStatements are the sentences TRUE of any host run (Expected=true, TierOrigin="host").
func hostTrueStatements() []ConsentStatement {
	return []ConsentStatement{
		{Text: "This agent can read and write every file your account can.", Expected: true, TierOrigin: "host"},
		{Text: "This agent can use your logged-in credentials.", Expected: true, TierOrigin: "host"},
		{Text: "This agent can reach any network your Mac can reach.", Expected: true, TierOrigin: "host"},
		{Text: "Nothing this agent does is sandboxed or contained.", Expected: true, TierOrigin: "host"},
	}
}

// crossTierDecoys are sentences TRUE for a more-isolated tier but FALSE for host (Expected=false). They
// are built from the same capability distinctions as EnvTier/RiskSummary, so a false statement is always
// constructible — even for a maximum-permission host profile (no all-Yes degradation).
func crossTierDecoys() []ConsentStatement {
	return []ConsentStatement{
		{Text: "This run is confined to the workspace and temp folders.", Expected: false, TierOrigin: "container"},
		{Text: "Files outside the project are invisible to this agent.", Expected: false, TierOrigin: "container"},
		{Text: "Network access is limited to an approved allow-list.", Expected: false, TierOrigin: "container"},
		{Text: "This agent runs in a disposable, throwaway environment.", Expected: false, TierOrigin: "container"},
	}
}

// HostConsentStatements draws the comprehension rows for one host launch. It samples count statements
// with a guaranteed mix — at least one TRUE host statement and at least one FALSE cross-tier decoy, so
// there is never a blind all-Yes or all-No muscle path — then shuffles them. r controls sampling so the
// caller owns determinism (production seeds from the clock; tests pass a fixed seed). count is raised to
// 2 if smaller, so the mix invariant always holds.
func HostConsentStatements(count int, r *rand.Rand) []ConsentStatement {
	if count < 2 {
		count = 2
	}
	trues := hostTrueStatements()
	falses := crossTierDecoys()
	r.Shuffle(len(trues), func(i, j int) { trues[i], trues[j] = trues[j], trues[i] })
	r.Shuffle(len(falses), func(i, j int) { falses[i], falses[j] = falses[j], falses[i] })

	out := []ConsentStatement{trues[0], falses[0]} // guarantee >=1 true and >=1 false
	rest := append(append([]ConsentStatement{}, trues[1:]...), falses[1:]...)
	r.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	for i := 0; len(out) < count && i < len(rest); i++ {
		out = append(out, rest[i])
	}
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
