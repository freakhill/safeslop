package creds

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// GithubCredsExpiry reports the earliest expiry across a session's staged GitHub
// App tokens by reading the value-free manifest at <stage>/git/github-meta.json
// (the same file RevokeGithub consults). ok is false when no github creds are
// staged (manifest absent) or the manifest carries no expiry; that is the normal
// case for sessions without a github account link, so it is not an error. The
// returned instant is the TTL ceiling the session's HTTPS access is capped at —
// the 1h App-token lifetime (specs/0069 T8).
func GithubCredsExpiry(stageDir string) (time.Time, bool, error) {
	b, err := os.ReadFile(filepath.Join(stageDir, githubDir, githubMetaFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	var meta githubMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return time.Time{}, false, err
	}
	if meta.MinExpiresAt == "" {
		return time.Time{}, false, nil
	}
	exp, err := time.Parse(time.RFC3339, meta.MinExpiresAt)
	if err != nil {
		return time.Time{}, false, err
	}
	return exp, true, nil
}
