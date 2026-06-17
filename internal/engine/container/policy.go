package container

import (
	"bufio"
	"bytes"
	"strings"
	"text/template"
)

// blockedNets are denied in BOTH modes (cloud metadata + RFC1918), mirroring squid's
// deny-first ACL. These prefixes are a test-oracle approximation of the CIDR blocks in
// squid.conf.tmpl; squid is the real enforcer.
var blockedNets = []string{"127.", "169.254.", "10.", "172.16.", "192.168."}

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
func Decide(domain, network string) bool {
	for _, n := range blockedNets {
		if strings.HasPrefix(domain, n) {
			return false
		}
	}
	for _, h := range blockedHosts {
		if domain == h {
			return false
		}
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
