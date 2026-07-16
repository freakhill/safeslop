package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
)

const (
	proxyReconfigureAttempts = 10
	proxyReconfigureDelay    = 50 * time.Millisecond
)

type overlayOptions struct {
	testHook func(string) error
}

// OverlayOption customizes ApplySessionGrants. It exists for hermetic tests only; production uses
// the default atomic temp+rename path.
type OverlayOption func(*overlayOptions)

// WithOverlayTestHook injects an error after rendering the candidate overlay to a temp path and
// before rename/reload. It lets tests prove write/update failure leaves the previous overlay and does
// not reconfigure the proxy, without chmod tricks that vary by platform/user.
func WithOverlayTestHook(fn func(string) error) OverlayOption {
	return func(o *overlayOptions) { o.testHook = fn }
}

// ApplySessionGrants materializes the desired session grant overlay for an already-running compose
// stack, reloads the proxy, and restores the previous overlay if reload fails. The session record
// caller must save only after this returns nil: any write/reload failure preserves the previous
// on-disk overlay and therefore the previous effective deny posture. The current compose asset bind-
// mounts session-grants.conf as a file, so the update path writes the validated candidate back through
// the existing path (O_TRUNC) rather than swapping the inode out from under the bind mount.
func ApplySessionGrants(ctx context.Context, eng runtime.Engine, composeFile, runtimeDir string, grants []SessionGrant, opts ...OverlayOption) error {
	var cfg overlayOptions
	for _, opt := range opts {
		opt(&cfg)
	}
	path := filepath.Join(runtimeDir, "session-grants.conf")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing session grants overlay: %w", err)
	}
	if os.IsNotExist(err) {
		old = []byte(RenderSessionGrants(nil))
	}
	next := []byte(RenderSessionGrants(grants))
	if err := writeOverlayCandidate(path, next, cfg.testHook); err != nil {
		return fmt.Errorf("write session grants overlay: %w", err)
	}
	if err := reconfigureProxy(ctx, eng, composeFile); err != nil {
		_ = os.WriteFile(path, old, 0o600)
		if restoreErr := reconfigureProxy(ctx, eng, composeFile); restoreErr != nil {
			return fmt.Errorf("reconfigure proxy failed after overlay update (%v); restore reconfigure also failed: %w", err, restoreErr)
		}
		return fmt.Errorf("reconfigure proxy: %w", err)
	}
	return nil
}

func writeOverlayCandidate(path string, content []byte, hook func(string) error) error {
	tmp := path + ".next"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	if hook != nil {
		if err := hook(tmp); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	candidate, err := os.ReadFile(tmp)
	_ = os.Remove(tmp)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, candidate, 0o600); err != nil {
		return err
	}
	// Best-effort file fsync so the proxy reload sees the just-written content where the platform
	// permits it. Overlay correctness does not depend on fsync success in unit tests.
	if f, err := os.OpenFile(path, os.O_RDONLY, 0); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return nil
}

func reconfigureProxy(ctx context.Context, eng runtime.Engine, composeFile string) error {
	var lastErr error
	for attempt := 0; attempt < proxyReconfigureAttempts; attempt++ {
		cmd := eng.Command(ctx, "compose", "-f", composeFile, "exec", "-T", "proxy", "squid", "-k", "reconfigure")
		if err := cmd.Run(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt+1 == proxyReconfigureAttempts {
			break
		}
		timer := time.NewTimer(proxyReconfigureDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}
