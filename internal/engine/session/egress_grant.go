package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
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

// ErrSessionNotGrantable is returned when a grant is requested for a session that cannot enforce
// one. Grants are only meaningful for environment:"container" + network:"deny" sessions: host
// sessions have no isolation boundary to scope, and network:"allow" sessions already have open
// egress (specs/0089/0097).
var ErrSessionNotGrantable = errors.New("network grants are only enforceable for container deny sessions")

// nonGrantableHosts are denied regardless of grant: cloud metadata hostnames and localhost. They
// mirror the proxy's hard-deny host set so a grant can never bypass metadata protections.
var nonGrantableHosts = map[string]bool{
	"localhost":                  true,
	"metadata.google.internal":   true,
	"metadata":                   true,
	"instance-data.ec2.internal": true,
}

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
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", 0, errors.New("egress grant: host is empty")
	}
	// A grant is one exact FQDN: reject anything that smuggles scheme/path/wildcard/encoding.
	for _, bad := range []string{"http://", "https://", "ftp://", "ws://", "wss://", "/", "?", "#", " ", "\t", "\n", "*", "%", "@"} {
		if strings.Contains(host, bad) {
			return "", 0, fmt.Errorf("egress grant: host %q must be an exact FQDN (no scheme/path/query/wildcard/encoding)", host)
		}
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", 0, fmt.Errorf("egress grant: host %q: leading/trailing dot (suffix/wildcard match) is not grantable; use the exact FQDN", host)
	}
	if isIPLiteral(host) {
		return "", 0, fmt.Errorf("egress grant: host %q: IP literals are non-grantable (use the FQDN)", host)
	}
	if nonGrantableHosts[host] {
		return "", 0, fmt.Errorf("egress grant: host %q: localhost/metadata destinations are non-grantable", host)
	}
	if port != 80 && port != 443 {
		return "", 0, fmt.Errorf("egress grant: port %d: only 80/443 are grantable in MVP (squid Safe_ports)", port)
	}
	return host, port, nil
}

// isIPLiteral reports whether host is an IPv4 or IPv6 literal (bare or bracketed). Mirrors the
// proxy's ip_literal_dst deny so a grant can never target a raw address (which bypasses the FQDN
// allowlist model and could name a private/metadata IP).
func isIPLiteral(host string) bool {
	h := strings.Trim(host, "[]")
	return net.ParseIP(h) != nil
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
	g := EgressGrant{
		ID:        newGrantID(),
		Host:      h,
		Port:      p,
		Source:    "operator",
		CreatedAt: now.UTC(),
	}
	sess.EgressGrants = append(sess.EgressGrants, g)
	sess.GrantRevision++
	sess.UpdatedAt = now.UTC()
	return sess, g, nil
}

// RevokeGrant removes the grant with the given ID and bumps the revision. An unknown ID is an
// error (the caller surfaces it rather than silently no-op'ing, so a stale UI does not look like
// success). Like AppendGrant it never touches profile policy.
func RevokeGrant(sess Session, id string, now time.Time) (Session, error) {
	if !CanGrant(sess) {
		return sess, ErrSessionNotGrantable
	}
	for i, g := range sess.EgressGrants {
		if g.ID == id {
			sess.EgressGrants = append(sess.EgressGrants[:i], sess.EgressGrants[i+1:]...)
			sess.GrantRevision++
			sess.UpdatedAt = now.UTC()
			return sess, nil
		}
	}
	return sess, fmt.Errorf("egress grant %q not found in session %s", id, sess.ID)
}

// newGrantID returns a short, non-secret, random grant id ("g-<6 hex>"). It is opaque and carries
// no host/port information; the (host,port) pair is stored on the grant itself.
func newGrantID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing is catastrophic for the process; fall back to a time-derived id so the
		// grant still lands rather than blocking an operator action.
		return fmt.Sprintf("g-%x", time.Now().UnixNano()&0xffffff)
	}
	return "g-" + hex.EncodeToString(b[:])
}
