package creds

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGithubCredsExpiry(t *testing.T) {
	stage := t.TempDir()
	// No manifest: absent github creds is the normal unlinked case, not an error.
	if _, ok, err := GithubCredsExpiry(stage); ok || err != nil {
		t.Fatalf("absent manifest: ok=%v err=%v", ok, err)
	}

	gitDir := filepath.Join(stage, githubDir)
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(42 * time.Minute).UTC().Truncate(time.Second)
	body := `{"host":"github.com","minExpiresAt":"` + want.Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(gitDir, githubMetaFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok, err := GithubCredsExpiry(stage)
	if err != nil || !ok {
		t.Fatalf("present manifest: ok=%v err=%v", ok, err)
	}
	if !got.Equal(want) {
		t.Fatalf("expiry: got %v want %v", got, want)
	}
}
