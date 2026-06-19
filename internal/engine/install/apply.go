package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Fetcher fetches an artifact URL. The CLI/server use HTTP; tests use a fake.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (io.ReadCloser, error)
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
	if err := extractTarGz(data, work); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	switch a.Format {
	case FormatBinaryTarball:
		return installBinary(a.Name, work, dirs.BinDir)
	case FormatAppTarball:
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
	return copyFile(src, filepath.Join(binDir, name), 0o755)
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
	_ = os.RemoveAll(dest)
	if err := os.Rename(app, dest); err != nil {
		if cerr := copyTree(app, dest); cerr != nil { // cross-device fallback
			return cerr
		}
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(binDir, name)
	_ = os.Remove(link)
	return os.Symlink(filepath.Join(dest, "Contents", "MacOS", name), link)
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

// safeJoin joins name under root and rejects anything that escapes root (path traversal).
func safeJoin(root, name string) (string, error) {
	clean := filepath.Join(root, name)
	rel, err := filepath.Rel(root, clean)
	if err != nil || filepathHasDotDotPrefix(rel) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	return clean, nil
}
