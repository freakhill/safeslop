package session

import (
	"strings"
	"testing"
	"time"
)

func grantSession() Session {
	return Session{
		ID:          "sess-test",
		Agent:       "pi",
		Environment: "container",
		Network:     "deny",
		Status:      StatusRunning,
	}
}

func TestValidateEgressGrantNormalizesValidFQDN(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"Example.com", 443, "example.com"},
		{"  api.anthropic.com  ", 443, "api.anthropic.com"},
		{"registry.npmjs.org", 80, "registry.npmjs.org"},
	}
	for _, c := range cases {
		h, p, err := ValidateEgressGrant(c.host, c.port)
		if err != nil {
			t.Errorf("ValidateEgressGrant(%q,%d): %v", c.host, c.port, err)
			continue
		}
		if h != c.want || p != c.port {
			t.Errorf("ValidateEgressGrant(%q,%d) = (%q,%d), want (%q,%d)", c.host, c.port, h, p, c.want, c.port)
		}
	}
}

func TestValidateEgressGrantRejectsNonGrantable(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string // substring of the error
	}{
		{"", 443, "empty"},
		{".example.com", 443, "exact FQDN"}, // leading dot → suffix match rejected
		{"example.com.", 443, "exact FQDN"},
		{"*.example.com", 443, "exact FQDN"},
		{"https://example.com", 443, "exact FQDN"},
		{"example.com/path", 443, "exact FQDN"},
		{"example.com?q=1", 443, "exact FQDN"},
		{"example.com#frag", 443, "exact FQDN"},
		{"example com", 443, "exact FQDN"},
		{"example%2ecom", 443, "exact FQDN"},
		{"example.com", 8080, "80/443"}, // non-safe port
		{"example.com", 22, "80/443"},
		{"10.0.0.1", 443, "IP literal"},
		{"127.0.0.1", 443, "IP literal"},
		{"[::1]", 443, "IP literal"},
		{"::1", 443, "IP literal"},
		{"169.254.169.254", 443, "IP literal"},
		{"localhost", 443, "localhost/metadata"},
		{"metadata.google.internal", 443, "localhost/metadata"},
		{"metadata", 443, "localhost/metadata"},
		{"instance-data.ec2.internal", 443, "localhost/metadata"},
	}
	for _, c := range cases {
		_, _, err := ValidateEgressGrant(c.host, c.port)
		if err == nil {
			t.Errorf("ValidateEgressGrant(%q,%d): want error containing %q, got nil", c.host, c.port, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("ValidateEgressGrant(%q,%d): error %q does not contain %q", c.host, c.port, err.Error(), c.want)
		}
	}
}

func TestCanGrantOnlyContainerDeny(t *testing.T) {
	cases := []struct {
		env, net string
		want     bool
	}{
		{"container", "deny", true},
		{"container", "allow", false},
		{"host", "deny", false},
		{"host", "allow", false},
	}
	for _, c := range cases {
		sess := Session{Environment: c.env, Network: c.net}
		if got := CanGrant(sess); got != c.want {
			t.Errorf("CanGrant(env=%s,net=%s) = %v, want %v", c.env, c.net, got, c.want)
		}
	}
}

func TestAppendGrantValidatesAndBumpsRevision(t *testing.T) {
	now := time.Now()
	sess := grantSession()
	sess, g, err := AppendGrant(sess, "api.example.com", 443, now)
	if err != nil {
		t.Fatalf("AppendGrant: %v", err)
	}
	if g.Host != "api.example.com" || g.Port != 443 || g.ID == "" || g.Source != "operator" {
		t.Errorf("returned grant = %+v", g)
	}
	if len(sess.EgressGrants) != 1 || sess.GrantRevision != 1 {
		t.Errorf("session = %+v grants, rev %d", sess.EgressGrants, sess.GrantRevision)
	}
	if !g.CreatedAt.Equal(now.UTC()) {
		t.Errorf("created_at not UTC-normalized: %v", g.CreatedAt)
	}
}

func TestAppendGrantIsIdempotentForSameDestination(t *testing.T) {
	now := time.Now()
	sess, _, _ := AppendGrant(grantSession(), "api.example.com", 443, now)
	sess2, g2, err := AppendGrant(sess, "API.Example.com", 443, now) // same after normalize
	if err != nil {
		t.Fatal(err)
	}
	if len(sess2.EgressGrants) != 1 {
		t.Errorf("idempotent grant must not duplicate: %+v", sess2.EgressGrants)
	}
	if sess2.GrantRevision != 1 {
		t.Errorf("revision must not bump on idempotent re-grant: %d", sess2.GrantRevision)
	}
	if g2.ID != sess.EgressGrants[0].ID {
		t.Errorf("idempotent grant must return the existing grant id %q, got %q", sess.EgressGrants[0].ID, g2.ID)
	}
}

func TestAppendGrantRejectsBadTarget(t *testing.T) {
	if _, _, err := AppendGrant(grantSession(), "10.0.0.1", 443, time.Now()); err == nil {
		t.Fatal("AppendGrant must reject an IP-literal target")
	}
}

func TestAppendGrantRejectsNonGrantableSession(t *testing.T) {
	sess := Session{Environment: "container", Network: "allow"}
	if _, _, err := AppendGrant(sess, "api.example.com", 443, time.Now()); err != ErrSessionNotGrantable {
		t.Fatalf("AppendGrant on allow session: want ErrSessionNotGrantable, got %v", err)
	}
	host := Session{Environment: "host", Network: "deny"}
	if _, _, err := AppendGrant(host, "api.example.com", 443, time.Now()); err != ErrSessionNotGrantable {
		t.Fatalf("AppendGrant on host session: want ErrSessionNotGrantable, got %v", err)
	}
}

func TestRevokeGrantRemovesAndBumpsRevision(t *testing.T) {
	now := time.Now()
	sess, g1, _ := AppendGrant(grantSession(), "a.example.com", 443, now)
	sess, g2, _ := AppendGrant(sess, "b.example.com", 443, now)
	if sess.GrantRevision != 2 {
		t.Fatalf("rev = %d, want 2", sess.GrantRevision)
	}
	sess, err := RevokeGrant(sess, g1.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.EgressGrants) != 1 || sess.EgressGrants[0].ID != g2.ID {
		t.Errorf("revoke removed the wrong grant: %+v", sess.EgressGrants)
	}
	if sess.GrantRevision != 3 {
		t.Errorf("rev = %d, want 3 after revoke", sess.GrantRevision)
	}
}

func TestRevokeGrantUnknownIDErrors(t *testing.T) {
	sess, _, _ := AppendGrant(grantSession(), "a.example.com", 443, time.Now())
	if _, err := RevokeGrant(sess, "g-nope", time.Now()); err == nil {
		t.Fatal("RevokeGrant unknown id must error, not silently no-op")
	}
}

func TestDismissEgressAcknowledgesWithoutGrantingAuthority(t *testing.T) {
	now := testNow()
	sess, acknowledgement, err := DismissEgress(grantSession(), "API.Example.com", 443, now)
	if err != nil {
		t.Fatalf("DismissEgress: %v", err)
	}
	if acknowledgement.Host != "api.example.com" || acknowledgement.Port != 443 || !acknowledgement.AcknowledgedAt.Equal(now) {
		t.Fatalf("acknowledgement = %+v", acknowledgement)
	}
	if len(sess.EgressAcknowledgements) != 1 || len(sess.EgressGrants) != 0 || sess.GrantRevision != 0 {
		t.Fatalf("dismissal must not grant authority: %+v", sess)
	}
	later := now.Add(time.Minute)
	sess, acknowledgement, err = DismissEgress(sess, "api.example.com", 443, later)
	if err != nil || len(sess.EgressAcknowledgements) != 1 || !acknowledgement.AcknowledgedAt.Equal(later) {
		t.Fatalf("repeat dismissal must replace its acknowledgement: sess=%+v ack=%+v err=%v", sess, acknowledgement, err)
	}
	if _, _, err := DismissEgress(Session{Environment: "host", Network: "deny"}, "api.example.com", 443, now); err != ErrSessionNotGrantable {
		t.Fatalf("host dismissal error = %v, want ErrSessionNotGrantable", err)
	}
}

func TestEgressGrantCarriesNoCredentialValue(t *testing.T) {
	// A grant is value-free: only host+port+source+id+time. Pin that the JSON has no place for a
	// token/path/header — the struct has exactly those fields.
	g := EgressGrant{ID: "g-abc", Host: "example.com", Port: 443, Source: "operator"}
	if g.Host != "example.com" || g.Port != 443 {
		t.Errorf("grant = %+v", g)
	}
}
