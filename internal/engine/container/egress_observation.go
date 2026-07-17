package container

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// EgressObservation is a value-free, proxy-denied destination seen for one
// container session. It never contains a URL path, query, request headers, or
// credentials; the operator must explicitly choose a separate grant action.
type EgressObservation struct {
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	LastSeen  time.Time `json:"last_seen"`
	Count     int       `json:"count"`
	Grantable bool      `json:"grantable"`
	Reason    string    `json:"reason,omitempty"`
}

// ParseDeniedEgressObservations converts the dedicated Squid log format
//
//	<unix-seconds.millis> <cache-status/http-status> <method> <domain> <port>
//
// into deduplicated operator observations. The Squid format deliberately logs
// the destination domain and port instead of a request URI, so paths, queries,
// request headers, and credentials cannot enter this data path.
func ParseDeniedEgressObservations(logs string) []EgressObservation {
	byTarget := make(map[string]EgressObservation)
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		seen, denied, _, host, port, ok := parseDeniedObservationLine(scanner.Text())
		if !ok || !denied {
			continue
		}
		key := host + "\x00" + strconv.Itoa(port)
		obs, exists := byTarget[key]
		if !exists {
			grantable, reason := observationGrantability(host, port)
			obs = EgressObservation{Host: host, Port: port, LastSeen: seen, Count: 1, Grantable: grantable, Reason: reason}
		} else {
			obs.Count++
			if seen.After(obs.LastSeen) {
				obs.LastSeen = seen
			}
		}
		byTarget[key] = obs
	}
	out := make([]EgressObservation, 0, len(byTarget))
	for _, obs := range byTarget {
		out = append(out, obs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host == out[j].Host {
			return out[i].Port < out[j].Port
		}
		return out[i].Host < out[j].Host
	})
	return out
}

// parseDeniedObservationLine accepts only the engine-generated format from
// squid.conf.tmpl. A malformed or old/default log entry is ignored rather than
// heuristically parsing a URI that could carry paths or query values.
func parseDeniedObservationLine(line string) (time.Time, bool, string, string, int, bool) {
	fields := strings.Fields(line)
	if len(fields) != 5 || !strings.HasPrefix(fields[1], "TCP_DENIED/") {
		return time.Time{}, false, "", "", 0, false
	}
	seen, ok := parseObservationTime(fields[0])
	if !ok {
		return time.Time{}, false, "", "", 0, false
	}
	method := fields[2]
	if method == "" {
		return time.Time{}, false, "", "", 0, false
	}
	host := strings.ToLower(strings.TrimSpace(fields[3]))
	// The custom format emits a domain, never a URI. Still reject malformed
	// input defensively so hostile request material cannot be reflected in JSON.
	if host == "" || host == "-" || strings.ContainsAny(host, "/?#@ \t\r\n") {
		return time.Time{}, false, "", "", 0, false
	}
	port, err := strconv.Atoi(fields[4])
	if err != nil || port < 1 || port > 65535 {
		return time.Time{}, false, "", "", 0, false
	}
	return seen, true, method, host, port, true
}

func parseObservationTime(raw string) (time.Time, bool) {
	whole, fraction, ok := strings.Cut(raw, ".")
	if !ok || whole == "" || fraction == "" || len(fraction) > 9 {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	for len(fraction) < 9 {
		fraction += "0"
	}
	nsec, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec).UTC(), true
}

func observationGrantability(host string, port int) (bool, string) {
	if _, _, err := engsession.ValidateEgressGrant(host, port); err == nil {
		return true, ""
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return false, "IP literal destinations are non-grantable"
	}
	switch host {
	case "localhost", "metadata", "metadata.google.internal", "instance-data.ec2.internal":
		return false, "localhost and metadata destinations are non-grantable"
	}
	if port != 80 && port != 443 {
		return false, "only ports 80 and 443 are grantable"
	}
	return false, "destination is not an exact grantable FQDN:port"
}

// ReadDeniedEgressObservations reads proxy logs through the active compose
// backend. A command/read failure returns no observations; this read-only path
// never updates a grant overlay or session record.
func ReadDeniedEgressObservations(ctx context.Context, eng runtime.Engine, composeFile string) ([]EgressObservation, error) {
	args, err := composeProjectArgs(composeFile, "logs", "--no-log-prefix", "proxy")
	if err != nil {
		return nil, err
	}
	cmd := eng.Command(ctx, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read proxy denied-request logs: %w", err)
	}
	return ParseDeniedEgressObservations(string(out)), nil
}
