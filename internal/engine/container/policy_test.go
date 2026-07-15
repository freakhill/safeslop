package container

import (
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		domain, network string
		want            bool
	}{
		{"api.anthropic.com", "deny", true},         // allowlisted (.anthropic.com)
		{"raw.githubusercontent.com", "deny", true}, // subdomain of .githubusercontent.com
		{"example.com", "deny", false},              // not allowlisted
		{"example.com", "allow", true},              // open
		{"93.184.216.34", "allow", true},            // open keeps public IP literals open
		{"169.254.169.254", "allow", false},         // metadata blocked even in allow
		{"10.0.0.5", "allow", false},                // RFC1918 blocked even in allow
		{"93.184.216.34", "deny", false},            // deny-tier IP literals cannot PTR-match allowlist
		{"2001:db8::1", "deny", false},              // same for IPv6 literals
		{"169.254.169.254", "deny", false},
	}
	for _, c := range cases {
		if got := Decide(c.domain, c.network); got != c.want {
			t.Errorf("Decide(%q,%q)=%v want %v", c.domain, c.network, got, c.want)
		}
	}
}

func TestRenderSquidConf(t *testing.T) {
	strict, err := RenderSquidConf(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strict, `dstdomain -n "/etc/squid/allowlist.domains"`) || !strings.Contains(strict, "http_access deny all") {
		t.Fatalf("strict squid.conf missing no-lookup allowlist/deny-all:\n%s", strict)
	}
	for _, want := range []string{"acl ip_literal_dst dstdom_regex -n", "http_access deny ip_literal_dst"} {
		if !strings.Contains(strict, want) {
			t.Fatalf("strict squid.conf missing %q:\n%s", want, strict)
		}
	}
	open, _ := RenderSquidConf(true)
	if !strings.Contains(open, "http_access allow all") {
		t.Fatalf("open squid.conf missing allow-all:\n%s", open)
	}
	// deny-first metadata/RFC1918 block must precede any allow line in both modes.
	for _, c := range []string{strict, open} {
		di := strings.Index(c, "http_access deny blocked_dst")
		ai := strings.Index(c, "http_access allow")
		if di < 0 || ai < 0 || di > ai {
			t.Fatalf("deny-first ordering broken:\n%s", c)
		}
	}
	id := strings.Index(strict, "http_access deny ip_literal_dst")
	ia := strings.Index(strict, "http_access allow allowed_domains")
	if id < 0 || ia < 0 || id > ia {
		t.Fatalf("IP-literal deny must precede domain allowlist:\n%s", strict)
	}
	if strings.Contains(open, "ip_literal_dst") {
		t.Fatalf("open squid.conf should not deny public IP literals:\n%s", open)
	}
}

// Decide is the test-oracle for squid; make it honest about metadata HOSTNAMES,
// not just the resolved link-local IP (squid blocks the IP after DNS; the oracle
// should refuse the hostname too so policy reasoning matches enforcement).
func TestDecideBlocksMetadataHostnames(t *testing.T) {
	for _, host := range []string{"metadata.google.internal", "metadata", "instance-data.ec2.internal"} {
		if Decide(host, "allow") {
			t.Fatalf("metadata hostname %q must be denied even in allow mode", host)
		}
	}
}

// TestDecideWithGrantsAllowsExactPairOnly pins specs/0097 T2: a session grant allows ONLY its
// exact FQDN:port pair in deny mode; a different port on the same host, or an ungranted host,
// stays denied. Hard denies (IP literals, private/metadata) cannot be overridden by a grant.
func TestDecideWithGrantsAllowsExactPairOnly(t *testing.T) {
	grants := []SessionGrant{{Host: "example.com", Port: 443}}
	cases := []struct {
		host    string
		port    int
		network string
		grants  []SessionGrant
		want    bool
	}{
		{"example.com", 443, "deny", grants, true},      // exact granted pair
		{"example.com", 80, "deny", grants, false},      // same host, wrong port
		{"sub.example.com", 443, "deny", grants, false}, // subdomain is NOT the exact FQDN
		{"other.com", 443, "deny", grants, false},       // not granted
		{"example.com", 443, "deny", nil, false},        // no grants => denied
		{"example.com", 443, "allow", grants, true},     // allow mode passes regardless
		// hard denies are never grantable:
		{"169.254.169.254", 443, "deny", []SessionGrant{{Host: "169.254.169.254", Port: 443}}, false},
		{"10.0.0.5", 443, "deny", []SessionGrant{{Host: "10.0.0.5", Port: 443}}, false},
		{"metadata.google.internal", 443, "deny", []SessionGrant{{Host: "metadata.google.internal", Port: 443}}, false},
		// an allowlisted domain still passes without a grant:
		{"api.anthropic.com", 443, "deny", nil, true},
	}
	for _, c := range cases {
		if got := DecideWithGrants(c.host, c.port, c.network, c.grants); got != c.want {
			t.Errorf("DecideWithGrants(%q,%d,%q,%v)=%v want %v", c.host, c.port, c.network, c.grants, got, c.want)
		}
	}
}

// TestRenderSessionGrantsEmitsExactACLs pins specs/0097 T2: each grant becomes one anchored
// dstdom_regex (exact FQDN, dots escaped) + a port ACL + an allow for the pair; empty grants
// yield a comment-only file so the unconditional include/mount always resolve.
func TestRenderSessionGrantsEmitsExactACLs(t *testing.T) {
	out := RenderSessionGrants([]SessionGrant{{Host: "example.com", Port: 443}, {Host: "api.anthropic.com", Port: 80}})
	if !strings.Contains(out, "acl grant_0_host dstdom_regex -n ^example\\.com$") {
		t.Errorf("grant 0 host ACL wrong (dots must be escaped, anchored):\n%s", out)
	}
	if !strings.Contains(out, "acl grant_0_port port 443") || !strings.Contains(out, "acl grant_1_port port 80") {
		t.Errorf("port ACLs missing:\n%s", out)
	}
	if strings.Count(out, "http_access allow grant_") != 2 {
		t.Errorf("each grant needs one allow line:\n%s", out)
	}
	if strings.Contains(out, "grant_0_host grant_1_port") {
		t.Errorf("host and port ACLs must be paired per-grant, not crossable:\n%s", out)
	}
	empty := RenderSessionGrants(nil)
	if !strings.HasPrefix(strings.TrimSpace(empty), "#") {
		t.Errorf("empty grants must render a comment-only file:\n%s", empty)
	}
}

// TestPersistentEgressUsesTheSameExactIncludeAndHardDeny proves durable rules
// use the same exact, hard-deny-preserving Squid include as session grants.
func TestPersistentEgressUsesTheSameExactIncludeAndHardDeny(t *testing.T) {
	persistent := []SessionGrant{{Host: "always.example.com", Port: 443}}
	if !DecideWithGrants("always.example.com", 443, "deny", persistent) {
		t.Fatal("persistent exact pair must be permitted through the shared include")
	}
	if DecideWithGrants("sub.always.example.com", 443, "deny", persistent) {
		t.Fatal("persistent rule must not permit a subdomain")
	}
	if DecideWithGrants("metadata.google.internal", 443, "deny", append(persistent, SessionGrant{Host: "metadata.google.internal", Port: 443})) {
		t.Fatal("hard denial must still win over a persistent exact entry")
	}
	out := RenderSessionGrants(persistent)
	if !strings.Contains(out, `^always\.example\.com$`) {
		t.Fatalf("persistent rule must render through the exact include:\\n%s", out)
	}
}

// TestSquidConfIncludesSessionGrantsBeforeDenyAll pins the include ordering: session-grants.conf
// is included after the hard denies + static allowlist and before the final deny-all, so a grant
// extends egress but cannot bypass a hard deny (specs/0097).
func TestSquidConfIncludesSessionGrantsBeforeDenyAll(t *testing.T) {
	strict, err := RenderSquidConf(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strict, "include /etc/squid/session-grants.conf") {
		t.Fatalf("squid.conf must include session-grants.conf:\n%s", strict)
	}
	inc := strings.Index(strict, "include /etc/squid/session-grants.conf")
	deny := strings.Index(strict, "http_access deny all")
	allow := strings.Index(strict, "http_access allow allowed_domains")
	ipDeny := strings.Index(strict, "http_access deny ip_literal_dst")
	if inc < 0 || deny < 0 || inc > deny {
		t.Fatalf("session-grants include must precede deny-all:\n%s", strict)
	}
	if ipDeny < 0 || allow < 0 || ipDeny > inc || allow > inc {
		t.Fatalf("hard denies + allowlist must precede the session-grants include:\n%s", strict)
	}
}
