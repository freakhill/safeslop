package cli

import (
	"encoding/json"
	"os"
	"time"

	"github.com/freakhill/safeslop/internal/engine/creds"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

func sessionData(sess engsession.Session) map[string]any {
	return sessionDataWithDeps(defaultDependencies(), sess)
}

func sessionDataWithDeps(d *dependencies, sess engsession.Session) map[string]any {
	out := map[string]any{
		"session_id":          sess.ID,
		"agent":               sess.Agent,
		"workspace":           sess.Workspace,
		"environment":         sess.Environment,
		"network":             sess.Network,
		"status":              sess.Status,
		"created_at":          sess.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":          sess.UpdatedAt.Format(time.RFC3339Nano),
		"credentials_revoked": sess.CredentialsRevoked,
	}
	if sess.Profile != "" {
		out["profile"] = sess.Profile
		out["profile_source"] = sess.ProfileSource
		out["policy_path"] = sess.PolicyPath
		out["policy_hash"] = sess.PolicyHash
	}
	if sess.Name != "" {
		out["name"] = sess.Name
	}
	if sess.RecipeID != "" {
		out["recipeID"] = sess.RecipeID
	}
	if sess.Image != "" {
		out["image"] = sess.Image
	}
	if sess.Resolved != nil {
		out["resolved"] = sess.Resolved
	}
	if len(sess.CredentialScopes) > 0 {
		out["credential_scopes"] = sess.CredentialScopes
	}
	if sess.CredentialLease != nil {
		lease := *sess.CredentialLease
		// A reconciled crash cannot leave a stale healthy lease in status. Until the last
		// known token expiry it is degraded/manager_unavailable; afterwards it is expired.
		if sess.LastError == "run process exited without recording status" && lease.State != string(creds.LeaseExpired) {
			if !lease.CurrentExpiresAt.IsZero() && !d.now().Before(lease.CurrentExpiresAt) {
				lease.State, lease.Reason = string(creds.LeaseExpired), "token_expired"
			} else {
				lease.State, lease.Reason = string(creds.LeaseDegraded), "manager_unavailable"
			}
		}
		out["credential_lease"] = &lease
	}
	if len(sess.PersistentEgress) > 0 {
		rows := make([]map[string]any, 0, len(sess.PersistentEgress))
		for _, rule := range sess.PersistentEgress {
			rows = append(rows, map[string]any{
				"fqdn": rule.FQDN, "port": rule.Port,
				"source": "profile-persistent", "lifetime": "future-sessions",
			})
		}
		out["persistent_egress"] = rows
	}
	if len(sess.EgressGrants) > 0 {
		out["egress_grants"] = sess.EgressGrants
	}
	if len(sess.EgressAcknowledgements) > 0 {
		out["egress_acknowledgements"] = sess.EgressAcknowledgements
	}
	if sess.GrantRevision > 0 {
		out["egress_grant_revision"] = sess.GrantRevision
	}
	if !sess.StartedAt.IsZero() {
		out["started_at"] = sess.StartedAt.Format(time.RFC3339Nano)
	}
	if !sess.StoppedAt.IsZero() {
		out["stopped_at"] = sess.StoppedAt.Format(time.RFC3339Nano)
	}
	if !sess.RevokedAt.IsZero() {
		out["revoked_at"] = sess.RevokedAt.Format(time.RFC3339Nano)
	}
	if sess.PID != 0 {
		out["pid"] = sess.PID
	}
	if sess.ExitCode != nil {
		out["exit_code"] = *sess.ExitCode
	}
	if sess.LastError != "" {
		out["last_error"] = sess.LastError
	}
	if sess.LastFailure != nil {
		out["last_failure"] = sess.LastFailure
	}
	if path, ok := d.sessionSocket(sess); ok {
		out["socket"] = path
	}
	return out
}

func emitContract(env jsoncontract.Envelope) {
	b, err := jsoncontract.Marshal(env)
	if err != nil {
		panic(err)
	}
	_, _ = os.Stdout.Write(b)
}

func emitContractLine(env jsoncontract.Envelope) {
	if err := jsoncontract.Validate(env); err != nil {
		panic(err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func emitContractError(code jsoncontract.ErrorCode, message string, details map[string]any) error {
	emitContract(jsoncontract.Error(jsoncontract.NewMessage(code, message, false, details)))
	return errOutputEmitted
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
