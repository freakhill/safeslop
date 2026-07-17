package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// EgressGrant is one operator-invoked, session-scoped, exact FQDN:port network grant for a
// container deny session (specs/0089/0097). It is runtime overlay state, never profile policy: it
// does not mutate profile.egress and is revoked with the session. It carries no credential values,
// URL paths, query strings, headers, request bodies, or secret refs — only the proxy-observed
// destination the operator explicitly approved.
type EgressGrant struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Source    string    `json:"source"` // "operator"
	CreatedAt time.Time `json:"created_at"`
}

// EgressAcknowledgement is a value-free, session-local review acknowledgement.
// It has no grant id or authority field because it must never be confused with
// a proxy rule (specs/0103).
type EgressAcknowledgement struct {
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

const (
	EgressDirectionWiden  = "widen"
	EgressDirectionNarrow = "narrow"
)

// EgressTransition is internal durable recovery state. CandidateGrants contains
// only the exact value-free session grant rows needed to identify/restore an
// interrupted narrowing; Session keeps it out of public JSON and v1 envelopes.
type EgressTransition struct {
	Direction         string        `json:"direction"`
	CandidateRevision int           `json:"candidate_revision"`
	CandidateHash     string        `json:"candidate_hash"`
	CandidateGrants   []EgressGrant `json:"candidate_grants,omitempty"`
}

type EgressRuntimeState struct {
	AppliedRevision int
	AppliedHash     string
	Transition      *EgressTransition
}

func (s Session) EgressRuntimeState() EgressRuntimeState {
	state := EgressRuntimeState{AppliedRevision: s.appliedEgressRevision, AppliedHash: s.appliedEgressHash}
	if s.egressTransition != nil {
		transition := *s.egressTransition
		transition.CandidateGrants = append([]EgressGrant(nil), transition.CandidateGrants...)
		state.Transition = &transition
	}
	return state
}

func (s *Session) SetEgressRuntimeState(state EgressRuntimeState) {
	s.appliedEgressRevision, s.appliedEgressHash = state.AppliedRevision, state.AppliedHash
	s.egressTransition = nil
	if state.Transition != nil {
		transition := *state.Transition
		transition.CandidateGrants = append([]EgressGrant(nil), transition.CandidateGrants...)
		s.egressTransition = &transition
	}
}

// ErrSessionNotGrantable is returned when a grant is requested for a session that cannot enforce
// one. Grants are only meaningful for environment:"container" + network:"deny" sessions: host
// sessions have no isolation boundary to scope, and network:"allow" sessions already have open
// egress (specs/0089/0097).
var ErrSessionNotGrantable = errors.New("network grants are only enforceable for container deny sessions")

// CanGrant reports whether a session can enforce an egress grant. Only container deny sessions
// route traffic through the squid proxy whose overlay a grant extends (specs/0089/0097).
func CanGrant(sess Session) bool {
	return sess.Environment == "container" && sess.Network == "deny"
}

// ValidateEgressGrant normalizes and validates an exact FQDN:port grant target, returning the
// normalized (lowercased host, port) or an error describing why the target is non-grantable. It is
// pure (no I/O) so the CLI can reuse the exact rule at grant time and in tests.
//
// Non-grantable targets (specs/0097 §Validation): IP literals (v4/v6/bracketed), private/link-local/
// metadata/localhost hosts, a leading dot or wildcard (suffix match), URL scheme/path/query/
// fragment/slash/space, and ports other than 80/443 (the squid Safe_ports MVP set).
func ValidateEgressGrant(host string, port int) (string, int, error) {
	normalized, normalizedPort, err := policy.ValidateExactEgress(host, port)
	if err != nil {
		// Preserve the stable session-grant error prefix consumed by the CLI's
		// value-minimal contract mapper while sharing policy validation.
		return "", 0, fmt.Errorf("egress grant: %w", err)
	}
	return normalized, normalizedPort, nil
}

// AppendGrant validates the target and the session posture, assigns a stable non-secret ID, and
// returns the session with the grant appended and the revision bumped. It never mutates profile
// policy. Duplicate (host,port) grants are idempotent: AppendGrant returns the existing grant
// without bumping the revision (re-granting the same destination changes nothing).
func AppendGrant(sess Session, host string, port int, now time.Time) (Session, EgressGrant, error) {
	if !CanGrant(sess) {
		return sess, EgressGrant{}, ErrSessionNotGrantable
	}
	h, p, err := ValidateEgressGrant(host, port)
	if err != nil {
		return sess, EgressGrant{}, err
	}
	for _, g := range sess.EgressGrants {
		if g.Host == h && g.Port == p {
			return sess, g, nil // idempotent: same destination already granted
		}
	}
	grantID, err := newGrantID(rand.Reader)
	if err != nil {
		return sess, EgressGrant{}, errors.New("egress grant id generation failed")
	}
	g := EgressGrant{
		ID:        grantID,
		Host:      h,
		Port:      p,
		Source:    "operator",
		CreatedAt: now.UTC(),
	}
	sess.EgressGrants = append(append([]EgressGrant(nil), sess.EgressGrants...), g)
	sess.GrantRevision++
	sess.UpdatedAt = now.UTC()
	return sess, g, nil
}

// RevokeGrant removes the grant with the given ID and bumps the revision. An unknown ID is an
// error (the caller surfaces it rather than silently no-op'ing, so a stale UI does not look like
// success). Like AppendGrant it never touches profile policy.
// DismissEgress records a session-local acknowledgement for one normalized exact
// destination. It deliberately shares the grantability boundary so acknowledgements
// cannot make host/open sessions appear enforceable, but it does not write or
// reload the proxy and does not alter grants or their revision.
func DismissEgress(sess Session, host string, port int, now time.Time) (Session, EgressAcknowledgement, error) {
	if !CanGrant(sess) {
		return sess, EgressAcknowledgement{}, ErrSessionNotGrantable
	}
	h, p, err := ValidateEgressGrant(host, port)
	if err != nil {
		return sess, EgressAcknowledgement{}, err
	}
	ack := EgressAcknowledgement{Host: h, Port: p, AcknowledgedAt: now.UTC()}
	for i, existing := range sess.EgressAcknowledgements {
		if existing.Host == h && existing.Port == p {
			sess.EgressAcknowledgements = append([]EgressAcknowledgement(nil), sess.EgressAcknowledgements...)
			sess.EgressAcknowledgements[i] = ack
			sess.UpdatedAt = now.UTC()
			return sess, ack, nil
		}
	}
	sess.EgressAcknowledgements = append(append([]EgressAcknowledgement(nil), sess.EgressAcknowledgements...), ack)
	sess.UpdatedAt = now.UTC()
	return sess, ack, nil
}

func RevokeGrant(sess Session, id string, now time.Time) (Session, error) {
	if !CanGrant(sess) {
		return sess, ErrSessionNotGrantable
	}
	for i, g := range sess.EgressGrants {
		if g.ID == id {
			grants := make([]EgressGrant, 0, len(sess.EgressGrants)-1)
			grants = append(grants, sess.EgressGrants[:i]...)
			grants = append(grants, sess.EgressGrants[i+1:]...)
			sess.EgressGrants = grants
			sess.GrantRevision++
			sess.UpdatedAt = now.UTC()
			return sess, nil
		}
	}
	return sess, fmt.Errorf("egress grant %q not found in session %s", id, sess.ID)
}

// newGrantID returns a short, non-secret, random grant id ("g-<6 hex>"). It is opaque and carries
// no host/port information; the (host,port) pair is stored on the grant itself. Entropy failure
// blocks the grant rather than risking a collision that could revoke the wrong authority row.
func newGrantID(reader io.Reader) (string, error) {
	var b [3]byte
	if _, err := io.ReadFull(reader, b[:]); err != nil {
		return "", err
	}
	return "g-" + hex.EncodeToString(b[:]), nil
}
