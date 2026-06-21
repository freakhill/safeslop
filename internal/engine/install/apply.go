package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Fetcher fetches an artifact URL. The CLI/server use HTTPFetcher; tests use a fake.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (io.ReadCloser, error)
}

// HTTPFetcher fetches over HTTPS with the default client, which on darwin consults the system
// trust store — so a corporate WARP CA installed in the keychain is honored (toolchain cert-env
// wiring for the tools we install is a separate slice; specs/0012 §10.3).
type HTTPFetcher struct{}

func (HTTPFetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

// DefaultDirs are the standard install targets: ~/.local/bin for executables, ~/Applications for
// .app bundles, and the OS temp dir for scratch.
func DefaultDirs() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{
		BinDir: filepath.Join(home, ".local", "bin"),
		AppDir: filepath.Join(home, "Applications"),
		TmpDir: os.TempDir(),
	}, nil
}

// Dirs are the install targets. BinDir gets executables (~/.local/bin); AppDir gets .app bundles
// (~/Applications); TmpDir is scratch for download/extract.
type Dirs struct {
	BinDir string
	AppDir string
	TmpDir string
}

// EventKind classifies an Apply progress event.
type EventKind string

const (
	EventStart    EventKind = "start"    // beginning an action
	EventProgress EventKind = "progress" // a step within an action (download/verify/install)
	EventDone     EventKind = "done"     // action completed
	EventError    EventKind = "error"    // action failed (fail-closed abort)
)

// Event is a pb-free progress record so the engine never imports the gRPC types; the CLI prints
// these and the control server translates them to pb.InstallApplyEvent.
type Event struct {
	Kind EventKind `json:"kind"`
	Tool string    `json:"tool"`
	Msg  string    `json:"msg"`
}

// The trust chain (specs/0012 §10.2 — the honest "no naive Homebrew" rationale):
// the pinned sha256es ship COMPILED INTO THE NOTARIZED safeslop binary, so an artifact that
// matches its embedded sha inherits Apple's code-signing root of trust — tampering with the pin
// set breaks the binary's own signature. A GitHub-release download against an advisory README hash
// (what brew-style delegation would amount to) is a WEAKER root of trust, not stronger. Where
// upstream also publishes a verifiable signature, Apply verifies it too (Pin.Sig), because a
// faithfully-checksummed artifact from a compromised maintainer is still malicious
// (provenance != honesty; TUF/SLSA). sha256 defends substitution/tampering; the signature defends
// maintainer compromise.
//
// Apply executes the plan's non-ok actions in manifest order: fetch -> verify (sha256, then the
// minisign chain when the pin declares a Sig) -> install per Format. Fail-closed: a verify error
// aborts that action (emitting EventError) and Apply returns the first error without installing.
func Apply(ctx context.Context, res Result, dirs Dirs, fetch Fetcher, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}
	for _, a := range res.Actions {
		if a.Kind == ActionOK {
			continue
		}
		emit(Event{Kind: EventStart, Tool: a.Name, Msg: string(a.Kind) + " " + a.Desired})
		if err := applyOne(ctx, a, dirs, fetch, emit); err != nil {
			emit(Event{Kind: EventError, Tool: a.Name, Msg: err.Error()})
			return fmt.Errorf("install %s: %w", a.Name, err)
		}
		emit(Event{Kind: EventDone, Tool: a.Name, Msg: a.Desired})
	}
	return nil
}

func applyOne(ctx context.Context, a Action, dirs Dirs, fetch Fetcher, emit func(Event)) error {
	emit(Event{Kind: EventProgress, Tool: a.Name, Msg: "downloading"})
	rc, err := fetch.Fetch(ctx, a.URL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("download read: %w", err)
	}
	emit(Event{Kind: EventProgress, Tool: a.Name, Msg: "verifying"})
	if err := VerifySHA256(data, a.SHA256); err != nil {
		return err
	}
	if a.Sig != nil {
		if err := verifySigChain(ctx, a, fetch); err != nil {
			return err
		}
	}
	emit(Event{Kind: EventProgress, Tool: a.Name, Msg: "installing"})
	work, err := os.MkdirTemp(dirs.TmpDir, "safeslop-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	switch a.Format {
	case FormatBinaryTarball:
		if err := extractTarGz(data, work); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		return installBinary(a.Name, work, dirs.BinDir)
	case FormatBinaryZip:
		if err := extractZip(data, work); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		return installBinary(a.Name, work, dirs.BinDir)
	case FormatAppTarball:
		if err := extractTarGz(data, work); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		return installApp(a.Name, work, dirs.AppDir, dirs.BinDir)
	default:
		return fmt.Errorf("unknown format %q", a.Format)
	}
}

// verifySigChain fetches the pin's checksum file + signature and verifies the minisign chain
// (filled in fully in SP7b-3 Task 7; sha256 alone is already enforced in applyOne).
func verifySigChain(ctx context.Context, a Action, fetch Fetcher) error {
	sumsRC, err := fetch.Fetch(ctx, a.Sig.SumsURL)
	if err != nil {
		return fmt.Errorf("fetch checksum file: %w", err)
	}
	sums, err := io.ReadAll(sumsRC)
	sumsRC.Close()
	if err != nil {
		return err
	}
	sigRC, err := fetch.Fetch(ctx, a.Sig.SigURL)
	if err != nil {
		return fmt.Errorf("fetch signature: %w", err)
	}
	sig, err := io.ReadAll(sigRC)
	sigRC.Close()
	if err != nil {
		return err
	}
	return VerifyMinisign(a.Sig.PubKey, sums, sig, a.SHA256, a.Sig.Artifact)
}

// installBinary copies <name>/bin/<name> (or the first file named <name>) into binDir, 0755.
func installBinary(name, srcRoot, binDir string) error {
	src := filepath.Join(srcRoot, name, "bin", name)
	if _, err := os.Stat(src); err != nil {
		found, ferr := findFile(srcRoot, name)
		if ferr != nil {
			return fmt.Errorf("binary %q not found in archive: %w", name, err)
		}
		src = found
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	// Stage beside the destination (same dir → same filesystem → the commit rename is atomic, so a
	// reader mid-exec never sees a torn file), keep any prior binary at <name>.bak for rollback, then
	// commit by rename; restore the backup on a commit failure.
	dest := filepath.Join(binDir, name)
	staged := dest + ".new"
	if err := copyFile(src, staged, 0o755); err != nil {
		return err
	}
	backup := dest + ".bak"
	_ = os.Remove(backup)
	hadOld := false
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("back up existing %q: %w", name, err)
		}
		hadOld = true
	}
	if err := os.Rename(staged, dest); err != nil {
		if hadOld {
			_ = os.Rename(backup, dest)
		}
		_ = os.Remove(staged)
		return fmt.Errorf("commit %q: %w", name, err)
	}
	return nil
}

// installApp moves <name>.app into appDir and symlinks its inner binary into binDir.
func installApp(name, srcRoot, appDir, binDir string) error {
	app := filepath.Join(srcRoot, name+".app")
	if _, err := os.Stat(app); err != nil {
		return fmt.Errorf("%s.app not found in archive: %w", name, err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(appDir, name+".app")
	staged := dest + ".new"
	_ = os.RemoveAll(staged)
	if err := os.Rename(app, staged); err != nil {
		if cerr := copyTree(app, staged); cerr != nil { // cross-device fallback
			return cerr
		}
	}
	// Keep the prior version for rollback instead of deleting it up front: a failed commit must never
	// leave the user with no app (the old destructive RemoveAll-then-rename lost the app on any failure).
	backup := dest + ".bak"
	_ = os.RemoveAll(backup)
	hadOld := false
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("back up existing %s.app: %w", name, err)
		}
		hadOld = true
	}
	if err := os.Rename(staged, dest); err != nil {
		if hadOld {
			_ = os.Rename(backup, dest) // roll back to the prior version
		}
		_ = os.RemoveAll(staged)
		return fmt.Errorf("commit %s.app: %w", name, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(binDir, name)
	_ = os.Remove(link)
	return os.Symlink(filepath.Join(dest, "Contents", "MacOS", name), link)
}

// Rollback restores the prior version of a tool that the last install kept at <dest>.bak (installApp /
// installBinary leave that backup behind on every upgrade). It swaps the current — presumably bad —
// version aside to <dest>.failed (kept, not destroyed, so the failure stays inspectable) and renames
// the .bak back into the live path. It handles both an .app bundle (AppDir/<name>.app.bak) and a bare
// binary (BinDir/<name>.bak), preferring whichever backup exists, and errors clearly when neither does.
// For an .app the BinDir symlink already targets the unchanged live path, so it stays valid.
func Rollback(name string, dirs Dirs) error {
	app := filepath.Join(dirs.AppDir, name+".app")
	bin := filepath.Join(dirs.BinDir, name)
	switch {
	case fileExists(app + ".bak"):
		return restoreBackup(app)
	case fileExists(bin + ".bak"):
		return restoreBackup(bin)
	default:
		return fmt.Errorf("no prior version of %q to roll back to", name)
	}
}

// restoreBackup sets the live path aside to live+".failed" (if present) and renames live+".bak" into
// place. Same-directory renames → atomic on the same filesystem, matching the install commit path.
func restoreBackup(live string) error {
	if fileExists(live) {
		failed := live + ".failed"
		_ = os.RemoveAll(failed)
		if err := os.Rename(live, failed); err != nil {
			return fmt.Errorf("set aside current version: %w", err)
		}
	}
	if err := os.Rename(live+".bak", live); err != nil {
		return fmt.Errorf("restore prior version: %w", err)
	}
	return nil
}

func extractTarGz(data []byte, dest string) error {
	gr, err := gzip.NewReader(bytesReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, h.Name) // reject path traversal
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// app bundles carry internal symlinks; only allow ones that stay inside dest.
			if _, err := safeJoin(filepath.Dir(target), h.Linkname); err != nil {
				return err
			}
			_ = os.Symlink(h.Linkname, target)
		}
	}
}

// extractZip unpacks a .zip (e.g. bun's release) into dest, rejecting path traversal and preserving
// the executable bit zip carries in its external attributes. Symlinks inside a zip are skipped (the
// binary releases we pin don't use them; allowing arbitrary zip symlinks is an escape vector).
func extractZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		target, err := safeJoin(dest, f.Name) // reject path traversal
		if err != nil {
			return err
		}
		info := f.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue // skip symlinks — not needed for the binary releases we pin
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm()|0o200)
		if err != nil {
			rc.Close()
			return err
		}
		_, cpErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cpErr != nil {
			return cpErr
		}
	}
	return nil
}

// safeJoin joins name under root and rejects anything that escapes root (path traversal).
func safeJoin(root, name string) (string, error) {
	clean := filepath.Join(root, name)
	rel, err := filepath.Rel(root, clean)
	if err != nil || filepathHasDotDotPrefix(rel) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	return clean, nil
}
