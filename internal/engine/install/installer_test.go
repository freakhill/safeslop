package install

import (
	"context"
	"os"
	"testing"
)

func TestFetchVerified(t *testing.T) {
	body := []byte("#!/bin/sh\necho rustup-init\n")
	url := "https://x/rustup-init"
	tmp := t.TempDir()

	// Good sha → a verified, executable temp file the caller can run.
	path, cleanup, err := FetchVerified(context.Background(), url, sha(body), tmp, fakeFetcher{url: body})
	if err != nil {
		t.Fatalf("FetchVerified (good sha): %v", err)
	}
	defer cleanup()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("verified installer must be an executable temp file: err=%v mode=%v", err, fi.Mode())
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Fatalf("verified installer content mismatch: %q", got)
	}

	// Bad sha → fail closed, nothing handed back.
	if p, _, err := FetchVerified(context.Background(), url, "00000000000000000000000000000000000000000000000000000000deadbeef", tmp, fakeFetcher{url: body}); err == nil || p != "" {
		t.Fatalf("FetchVerified must fail closed on sha mismatch, got path=%q err=%v", p, err)
	}
}
