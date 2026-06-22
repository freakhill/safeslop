package uninstall

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/freakhill/safeslop/internal/engine/install"
)

var errEmptyArgv = errors.New("uninstall: empty argv")

// ErrNeedsConfirm is returned by a Path A apply when a self-updating tool's on-disk hash no longer
// matches the receipt (expected for claude). It is NOT a tamper error — the caller must obtain explicit
// confirmation and re-run with ConfirmSelfUpdated set, rather than silently deleting or blessing the new
// hash (specs/0040/0041).
var ErrNeedsConfirm = errors.New("uninstall: self-updated tool needs explicit confirmation to remove")

// Result records the outcome of removing one item, for the CLI to report.
type Result struct {
	Tool       string
	Kind       Kind
	Trashed    []string // Path A: artifacts moved to trash
	Skipped    []string // paths intentionally left (missing, external symlink, ...) with reasons
	Notes      []string // warnings (running instances, codesign skipped, residue) — non-fatal
	TrashStamp string   // Path A: stamp for `uninstall rollback`
}

// Runner runs an argv and returns its combined stdout+stderr, the process exit code, and any error
// starting it. It is a seam so tests exercise the apply logic without shelling out to real installers.
type Runner func(ctx context.Context, argv []string) (output string, exitCode int, err error)

// defaultRunner executes argv for real, capturing combined output and the exit code.
func defaultRunner(ctx context.Context, argv []string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, errEmptyArgv
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			err = nil // a non-zero exit is reported via code, not err; err is reserved for start failures
		} else {
			code = -1
		}
	}
	return buf.String(), code, err
}

// Engine carries the injectable dependencies for an uninstall. The zero value is unusable; use
// NewEngine (production) or construct directly with fakes in tests.
type Engine struct {
	Dirs     install.Dirs
	Run      Runner                                    // how to run delegate uninstallers + probes
	Codesign func(ctx context.Context, p string) error // execution-time delegate re-verification
	Now      func() time.Time                          // trash-stamp clock
}

// NewEngine wires the production dependencies: real process exec, real codesign verification, real
// clock, and the standard install dirs.
func NewEngine(dirs install.Dirs) *Engine {
	return &Engine{
		Dirs:     dirs,
		Run:      defaultRunner,
		Codesign: install.VerifyCodesign,
		Now:      time.Now,
	}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// ApplyItem removes one planned item by its discipline: Path A own-and-remove (trash-recoverable) or
// Path B delegate-and-verify. confirmSelfUpdated authorises removing a Path A tool whose on-disk hash
// drifted as expected (claude); it is ignored for Path B.
func (e *Engine) ApplyItem(ctx context.Context, item Item, confirmSelfUpdated bool) (Result, error) {
	if item.Kind == DelegatePathB {
		return e.applyPathB(ctx, item)
	}
	return e.applyPathA(item, confirmSelfUpdated)
}
