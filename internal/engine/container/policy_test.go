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
