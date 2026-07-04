package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.github.com"
	apiVersion     = "2022-11-28"
	maxReposPerTok = 500 // GitHub caps repositories per installation-token request (specs/0068 G2)
)

// Client mints and revokes installation tokens over a ForgeHTTP seam. apiBase defaults to
// https://api.github.com; tests point it at httptest.
type Client struct {
	http    ForgeHTTP
	apiBase string
}

// New wraps a ForgeHTTP with an apiBase (empty => the public GitHub API).
func New(h ForgeHTTP, apiBase string) *Client {
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	return &Client{http: h, apiBase: strings.TrimRight(apiBase, "/")}
}

func appHeaders(jwt string) map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + jwt,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": apiVersion,
	}
}

// Installation is the non-secret metadata of GET /app/installations/{id}.
type Installation struct {
	ID         int    `json:"id"`
	AppID      int    `json:"app_id"`
	AppSlug    string `json:"app_slug"`
	TargetType string `json:"target_type"`
	Account    struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

// AccountLogin is the owner login the installation belongs to (drives `creds link github` owner
// derivation).
func (i *Installation) AccountLogin() string { return i.Account.Login }

// InstallationInfo probes GET /app/installations/{id} with an App JWT. It mints nothing; it only
// returns non-secret metadata used for owner derivation and status probes.
func (c *Client) InstallationInfo(ctx context.Context, appID, instID int, keyPEM []byte) (*Installation, error) {
	jwt, err := AppJWT(appID, keyPEM, time.Now())
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/app/installations/%d", c.apiBase, instID)
	body, status, err := c.http.Do(ctx, http.MethodGet, url, appHeaders(jwt), nil)
	if err != nil {
		return nil, fmt.Errorf("github: installation probe transport error: %w", err)
	}
	switch {
	case status == http.StatusNotFound:
		return nil, fmt.Errorf("github: installation %d not found — check --installation-id and that the App is installed", instID)
	case status/100 != 2:
		return nil, fmt.Errorf("github: installation probe failed (HTTP %d)", status)
	}
	var inst Installation
	if err := json.Unmarshal(body, &inst); err != nil {
		return nil, errors.New("github: could not parse installation response")
	}
	return &inst, nil
}

// MintRequest is one token request: an exact repo-name list (owner stripped) and a token-wide
// permission set. Empty Repositories mints an installation-wide token (avoid in staging).
type MintRequest struct {
	Repositories []string          // repo names without owner, e.g. ["api", "web"]
	Permissions  map[string]string // e.g. {"contents":"write","metadata":"read"}
}

// Token is a minted installation token and its hard expiry.
type Token struct {
	Token     string
	ExpiresAt time.Time
}

// MintToken POSTs /app/installations/{id}/access_tokens for exactly req.Repositories with
// req.Permissions. Default-deny: empty permissions are refused (C4). >500 repos is a hard error
// (G2). 422/404 (the App is not installed on a requested repo) maps to install guidance. No token
// byte is ever placed in an error string.
func (c *Client) MintToken(ctx context.Context, appID, instID int, keyPEM []byte, req MintRequest) (*Token, error) {
	if len(req.Permissions) == 0 {
		return nil, errors.New("github: refusing to mint a token with no permissions (default-deny)")
	}
	if len(req.Repositories) > maxReposPerTok {
		return nil, fmt.Errorf("github: too many repositories for one token (%d > %d)", len(req.Repositories), maxReposPerTok)
	}
	jwt, err := AppJWT(appID, keyPEM, time.Now())
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"permissions": req.Permissions}
	if len(req.Repositories) > 0 {
		payload["repositories"] = req.Repositories
	}
	reqBody, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBase, instID)
	body, status, err := c.http.Do(ctx, http.MethodPost, url, appHeaders(jwt), reqBody)
	if err != nil {
		return nil, fmt.Errorf("github: token mint transport error: %w", err)
	}
	switch {
	case status == http.StatusUnprocessableEntity || status == http.StatusNotFound:
		return nil, fmt.Errorf("github: the App installation cannot access one or more of [%s] — install the GitHub App on them (HTTP %d)", strings.Join(req.Repositories, ", "), status)
	case status/100 != 2:
		return nil, fmt.Errorf("github: token mint failed (HTTP %d)", status)
	}
	var out struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.New("github: could not parse token response")
	}
	if out.Token == "" {
		return nil, errors.New("github: token response contained no token")
	}
	exp, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		return nil, errors.New("github: could not parse token expiry")
	}
	return &Token{Token: out.Token, ExpiresAt: exp}, nil
}

// Revoke best-effort deletes the current installation token (DELETE /installation/token, authed
// with the token itself). 401/404 mean it is already dead and count as success; other non-2xx are
// returned for logging. The token value is never echoed.
func (c *Client) Revoke(ctx context.Context, token string) error {
	url := c.apiBase + "/installation/token"
	headers := map[string]string{
		"Authorization":        "Bearer " + token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": apiVersion,
	}
	_, status, err := c.http.Do(ctx, http.MethodDelete, url, headers, nil)
	if err != nil {
		return fmt.Errorf("github: token revoke transport error: %w", err)
	}
	switch status {
	case http.StatusNoContent, http.StatusUnauthorized, http.StatusNotFound:
		return nil
	}
	return fmt.Errorf("github: token revoke failed (HTTP %d)", status)
}
