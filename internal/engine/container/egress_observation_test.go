package container

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseDeniedEgressObservationsDedupesValueFreeTargets(t *testing.T) {
	logs := strings.Join([]string{
		"1720000000.123 TCP_DENIED/403 CONNECT api.example.com 443",
		"1720000001.456 TCP_DENIED/403 CONNECT api.example.com 443",
		"1720000002.789 TCP_DENIED/403 GET web.example.com 80",
		"1720000003.000 TCP_MISS/200 GET ignored.example.com 80",
	}, "\n")

	got := ParseDeniedEgressObservations(logs)
	if len(got) != 2 {
		t.Fatalf("observations = %#v, want two denied targets", got)
	}
	if got[0].Host != "api.example.com" || got[0].Port != 443 || got[0].Count != 2 || !got[0].Grantable {
		t.Fatalf("first observation = %#v, want deduped grantable api.example.com:443", got[0])
	}
	if want := time.Unix(1720000001, 456000000).UTC(); !got[0].LastSeen.Equal(want) {
		t.Fatalf("api last seen = %s, want %s", got[0].LastSeen, want)
	}
	if got[1].Host != "web.example.com" || got[1].Port != 80 || got[1].Count != 1 || !got[1].Grantable {
		t.Fatalf("second observation = %#v, want grantable web.example.com:80", got[1])
	}
}

func TestParseDeniedEgressObservationsMarksHardDeniedTargetsNonGrantable(t *testing.T) {
	logs := strings.Join([]string{
		"1720000000.123 TCP_DENIED/403 CONNECT 127.0.0.1 443",
		"1720000001.456 TCP_DENIED/403 CONNECT 169.254.169.254 80",
		"1720000002.789 TCP_DENIED/403 CONNECT metadata.google.internal 443",
	}, "\n")

	got := ParseDeniedEgressObservations(logs)
	if len(got) != 3 {
		t.Fatalf("observations = %#v, want three denied targets", got)
	}
	for _, obs := range got {
		if obs.Grantable || obs.Reason == "" {
			t.Fatalf("hard-denied observation = %#v, want non-grantable reason", obs)
		}
	}
	if !strings.Contains(got[0].Reason, "IP literal") || !strings.Contains(got[2].Reason, "metadata") {
		t.Fatalf("non-grantable reasons = %#v", got)
	}
}

func TestSquidEgressObservationLogFormatExcludesRequestURIs(t *testing.T) {
	conf, err := RenderSquidConf(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"logformat safeslop_observation %ts.%03tu %Ss/%03>Hs %>rm %>rd %>rP",
		"access_log stdio:/var/log/squid/access.log safeslop_observation",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("squid.conf missing value-free observation log format %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "safeslop_observation %ru") || strings.Contains(conf, "safeslop_observation %>ru") {
		t.Fatalf("observation log format must not record request URIs:\n%s", conf)
	}
}

func TestReadDeniedEgressObservationsFailureReturnsNoObservations(t *testing.T) {
	eng := newFakeEngine(t, nil)
	composeFile := "/runtime/compose.yml"
	eng.fail("compose -f "+composeFile+" logs --no-log-prefix proxy", 17)

	got, err := ReadDeniedEgressObservations(context.Background(), eng, composeFile)
	if err == nil {
		t.Fatal("ReadDeniedEgressObservations unexpectedly succeeded")
	}
	if len(got) != 0 {
		t.Fatalf("observation failure must return no observations, got %#v", got)
	}
	eng.assertRan(t, "compose -f "+composeFile+" logs --no-log-prefix proxy")
}
