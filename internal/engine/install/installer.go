package install

import (
	"context"
	"fmt"
	"io"
	"os"
)

// FetchVerified downloads url, verifies the bytes against sha256hex (fail-closed), writes them to a
// fresh executable temp file under tmpDir, and returns its path + a cleanup. It deliberately does NOT
// run anything — the caller is the executor. This is the verify half of the "verified installer" route
// (specs/0036 Task 6): a tool whose upstream install is `curl … | sh` actually just fetches a versioned
// installer binary (rustup-init, nix-installer) and runs it; pinning + checksum-verifying that binary
// here replaces the unverified remote script with the same Route A trust the placed-binary pins get.
// The install package stays a fetch+verify engine — executing the foreign installer is the tools layer's
// job, where running install commands already lives.
func FetchVerified(ctx context.Context, url, sha256hex, tmpDir string, fetch Fetcher) (path string, cleanup func(), err error) {
	rc, err := fetch.Fetch(ctx, url)
	if err != nil {
		return "", nil, fmt.Errorf("download: %w", err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return "", nil, fmt.Errorf("download read: %w", err)
	}
	if err := VerifySHA256(data, sha256hex); err != nil {
		return "", nil, err // fail closed: never hand back an unverified installer
	}
	f, err := os.CreateTemp(tmpDir, "safeslop-installer-*")
	if err != nil {
		return "", nil, err
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
