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
		{"169.254.169.254", "allow", false},         // metadata blocked even in allow
		{"10.0.0.5", "allow", false},                // RFC1918 blocked even in allow
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
	if !strings.Contains(strict, `dstdomain "/etc/squid/allowlist.domains"`) || !strings.Contains(strict, "http_access deny all") {
		t.Fatalf("strict squid.conf missing allowlist/deny-all:\n%s", strict)
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
}
