package container

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"text/template"

	"github.com/freakhill/safeslop/internal/engine/egress"
)

// blockedNets are denied in BOTH modes (cloud metadata + RFC1918), mirroring squid's
// deny-first ACL. These prefixes are a test-oracle approximation of the CIDR blocks in
// squid.conf.tmpl; squid is the real enforcer.
var blockedNets = []string{"127.", "169.254.", "10.", "172.16.", "192.168."}

// SessionGrant is the minimal exact-FQDN:port view of a session egress grant that the proxy
// overlay needs (specs/0097). It is a local type so the container package's render/oracle stay
// decoupled from the session record; the CLI maps session.EgressGrant -> SessionGrant.
type SessionGrant struct {
	Host string
	Port int
}

// hardDenied mirrors squid's deny-first ACLs (metadata + private ranges + blocked hostnames),
// which apply in BOTH modes and cannot be overridden by a session grant (specs/0097: IP literal /
// private / metadata destinations are non-grantable).
func hardDenied(host, network string) bool {
	for _, n := range blockedNets {
		if strings.HasPrefix(host, n) {
			return true
		}
	}
	for _, h := range blockedHosts {
		if host == h {
			return true
		}
	}
	if network != "allow" && isIPLiteral(host) {
		return true
	}
	return false
}

// blockedHosts are denied in BOTH modes: the cloud metadata hostnames that resolve
// to the link-local IP squid already blocks. Listed explicitly so the oracle (and a
// future non-IP enforcer) refuses the name, not only the resolved address.
var blockedHosts = []string{"metadata.google.internal", "metadata", "instance-data.ec2.internal"}

// BuildAllowlist returns the embedded allowlist domains (one per line; comments/blanks dropped).
func BuildAllowlist() ([]string, error) {
	b, err := readAsset("allowlist.domains")
	if err != nil {
		return nil, err
	}
	var out []string
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, s.Err()
}

// Decide mirrors squid's enforcement so the policy is testable without a running proxy:
// a literal IP in a blocked range is always denied; in "deny" only allowlisted domains pass
// (a leading-dot entry matches the domain and its subdomains); "allow" passes everything else.
// Strict-mode squid also denies public IP literals before its no-reverse-DNS domain allowlist.
func Decide(domain, network string) bool {
	if hardDenied(domain, network) {
		return false
	}
	if network == "allow" {
		return true
	}
	allow, err := BuildAllowlist()
	if err != nil {
		return false
	}
	for _, a := range allow {
		bare := strings.TrimPrefix(a, ".")
		if domain == bare || strings.HasSuffix(domain, "."+bare) {
			return true
		}
	}
	return false
}

// DecideWithGrants extends Decide with operator-invoked session egress grants (specs/0097): a
// granted exact FQDN:port pair is allowed in deny mode in addition to the static allowlist. Hard
// denies (IP literals, private/metadata, blocked hosts) still apply first and can NEVER be
// overridden by a grant — mirroring squid's deny-first ordering where session-grants.conf is
// included after the hard-deny ACLs and before the final deny-all.
func DecideWithGrants(host string, port int, network string, grants []SessionGrant) bool {
	if hardDenied(host, network) {
		return false
	}
	if network == "allow" {
		return true
	}
	for _, g := range grants {
		if host == g.Host && port == g.Port {
			return true
		}
	}
	allow, err := BuildAllowlist()
	if err != nil {
		return false
	}
	for _, a := range allow {
		bare := strings.TrimPrefix(a, ".")
		if host == bare || strings.HasSuffix(host, "."+bare) {
			return true
		}
	}
	return false
}

type EgressGeneration = egress.Generation

// BuildEgressGeneration returns the exact overlay bytes and their generation
// hash. The revision comment makes equal rule sets at distinct durable
// generations independently acknowledgeable.
func BuildEgressGeneration(grants []SessionGrant, revision int) (EgressGeneration, []byte, error) {
	return egress.Build(egressDestinations(grants), revision)
}

// RenderSessionGrants renders the squid session-grants include file: one exact dstdom_regex ACL
// pair per grant (host + port), then an http_access allow for the pair. Dots in the host are
// regex-escaped and the name is anchored so the match is EXACT (squid dstdomain on a bare name
// would also match subdomains; grants must be the single approved FQDN). Empty => a comment-only
// file so the unconditional include + bind mount always resolve.
func RenderSessionGrants(grants []SessionGrant) string {
	return egress.Render(egressDestinations(grants))
}

func egressDestinations(grants []SessionGrant) []egress.Destination {
	if len(grants) == 0 {
		return nil
	}
	destinations := make([]egress.Destination, len(grants))
	for i, grant := range grants {
		destinations[i] = egress.Destination{Host: grant.Host, Port: grant.Port}
	}
	return destinations
}

func isIPLiteral(host string) bool {
	return net.ParseIP(strings.Trim(host, "[]")) != nil
}

// RenderSquidConf renders squid.conf for the given mode (open == network "allow").
func RenderSquidConf(open bool) (string, error) {
	raw, err := readAsset("squid.conf.tmpl")
	if err != nil {
		return "", err
	}
	t, err := template.New("squid").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, struct{ Open bool }{open}); err != nil {
		return "", err
	}
	return b.String(), nil
}
