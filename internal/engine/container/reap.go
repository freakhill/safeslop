package container

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

const managedLabel = "safeslop.managed=true"

// ReapBySession removes safeslop-managed containers and networks that carry a session label.
// It is intentionally record-independent: a SIGKILL'd wrapper may have lost its session JSON,
// but the boundary still carries safeslop.session=<id> labels (specs/0055 W3 Bug A).
func ReapBySession(ctx context.Context, eng ReapEngine, sessionID string) error {
	return reapByOwnership(ctx, eng, "safeslop.session", sessionID)
}

var invocationIDPattern = regexp.MustCompile(`^run-[0-9a-f]{32}$`)

// ReapByInvocation targets one random direct-run ownership label. It never
// accepts a profile name or partial id, so cleanup authority cannot collide.
func ReapByInvocation(ctx context.Context, eng ReapEngine, invocationID string) error {
	if !invocationIDPattern.MatchString(invocationID) {
		return fmt.Errorf("direct invocation identity is invalid")
	}
	return reapByOwnership(ctx, eng, "safeslop.invocation", invocationID)
}

func reapByOwnership(ctx context.Context, eng ReapEngine, label, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	filter := "label=" + label + "=" + id
	managedFilter := "label=" + managedLabel
	containers, err := engineLines(ctx, eng, "ps", "-aq", "--filter", managedFilter, "--filter", filter)
	if err != nil {
		return fmt.Errorf("list owned containers: %w", err)
	}
	if len(containers) > 0 {
		args := append([]string{"rm", "-f"}, containers...)
		if err := eng.Command(ctx, args...).Run(); err != nil {
			// Compose `run --rm` may win the race after the list. Re-list the
			// exact random/session owner: absence is already the desired result.
			remaining, verifyErr := engineLines(ctx, eng, "ps", "-aq", "--filter", managedFilter, "--filter", filter)
			if verifyErr != nil || len(remaining) != 0 {
				return fmt.Errorf("remove owned containers: %w", err)
			}
		}
	}
	networks, err := engineLines(ctx, eng, "network", "ls", "-q", "--filter", managedFilter, "--filter", filter)
	if err != nil {
		return fmt.Errorf("list owned networks: %w", err)
	}
	if len(networks) > 0 {
		args := append([]string{"network", "rm"}, networks...)
		if err := eng.Command(ctx, args...).Run(); err != nil {
			remaining, verifyErr := engineLines(ctx, eng, "network", "ls", "-q", "--filter", managedFilter, "--filter", filter)
			if verifyErr != nil || len(remaining) != 0 {
				return fmt.Errorf("remove owned networks: %w", err)
			}
		}
	}
	return nil
}

// ReapManaged removes every safeslop-managed container and network. It powers `safeslop down`,
// where the point is a complete host-engine cleanup rather than a single session teardown.
func ReapManaged(ctx context.Context, eng ReapEngine) error {
	containers, err := engineLines(ctx, eng, "ps", "-aq", "--filter", "label="+managedLabel)
	if err != nil {
		return fmt.Errorf("list managed containers: %w", err)
	}
	if len(containers) > 0 {
		args := append([]string{"rm", "-f"}, containers...)
		if err := eng.Command(ctx, args...).Run(); err != nil {
			remaining, verifyErr := engineLines(ctx, eng, "ps", "-aq", "--filter", "label="+managedLabel)
			if verifyErr != nil || len(remaining) != 0 {
				return fmt.Errorf("remove managed containers: %w", err)
			}
		}
	}
	networks, err := engineLines(ctx, eng, "network", "ls", "-q", "--filter", "label="+managedLabel)
	if err != nil {
		return fmt.Errorf("list managed networks: %w", err)
	}
	if len(networks) > 0 {
		args := append([]string{"network", "rm"}, networks...)
		if err := eng.Command(ctx, args...).Run(); err != nil {
			remaining, verifyErr := engineLines(ctx, eng, "network", "ls", "-q", "--filter", "label="+managedLabel)
			if verifyErr != nil || len(remaining) != 0 {
				return fmt.Errorf("remove managed networks: %w", err)
			}
		}
	}
	return nil
}

const invocationMarkerVersion = 1

type invocationMarker struct {
	Version      int    `json:"version"`
	InvocationID string `json:"invocation_id"`
	PID          int    `json:"pid"`
	ProcessToken string `json:"process_token"`
}

// WriteInvocationMarker records only non-secret process identity needed to
// distinguish a live direct wrapper from a dead/reused PID after a crash.
func WriteInvocationMarker(stageDir, invocationID string, pid int, processToken string) error {
	if !invocationIDPattern.MatchString(invocationID) || filepath.Base(stageDir) != invocationID || pid <= 0 {
		return fmt.Errorf("direct invocation marker is invalid")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(invocationMarker{Version: invocationMarkerVersion, InvocationID: invocationID, PID: pid, ProcessToken: processToken})
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(stageDir, ".invocation-marker-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if n, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	} else if n != len(body) {
		_ = tmp.Close()
		return io.ErrShortWrite
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filepath.Join(stageDir, ".safeslop-stage")); err != nil {
		return err
	}
	dir, err := os.Open(stageDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// RetainInvocationMarker wipes staged bearer/config files while preserving the
// exact cleanup handle after a normal-run reap could not be proven.
func RetainInvocationMarker(stageDir string) error {
	markerPath := filepath.Join(stageDir, ".safeslop-stage")
	body, err := os.ReadFile(markerPath)
	if err != nil {
		return err
	}
	var marker invocationMarker
	if json.Unmarshal(body, &marker) != nil || marker.Version != invocationMarkerVersion || marker.InvocationID != filepath.Base(stageDir) || marker.PID <= 0 {
		return fmt.Errorf("direct invocation marker is invalid")
	}
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".safeslop-stage" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(stageDir, entry.Name())); err != nil {
			return err
		}
	}
	dir, err := os.Open(stageDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// SweepDeadInvocations reaps only exact random invocation labels whose private
// marker proves the wrapper PID is dead or reused. It never signals a host PID.
func SweepDeadInvocations(ctx context.Context, eng ReapEngine, stageRoot string) error {
	entries, err := os.ReadDir(stageRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !invocationIDPattern.MatchString(id) {
			continue
		}
		markerPath := filepath.Join(stageRoot, id, ".safeslop-stage")
		info, err := os.Lstat(markerPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			continue
		}
		body, err := os.ReadFile(markerPath)
		if err != nil {
			continue
		}
		var marker invocationMarker
		if json.Unmarshal(body, &marker) != nil || marker.Version != invocationMarkerVersion || marker.InvocationID != id || marker.PID <= 0 {
			continue
		}
		process := engsession.Session{PID: marker.PID, ProcessToken: marker.ProcessToken}
		if engsession.ProcessAliveSession(process) {
			continue
		}
		if err := ReapByInvocation(ctx, eng, id); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(stageRoot, id)); err != nil {
			return err
		}
	}
	return nil
}

// SweepManagedOrphans reaps labelled boundaries whose safeslop.session label no longer has a live
// session record. It is safe to run at startup: live session ids are supplied by the session store;
// unlabelled legacy containers are ignored because safeslop cannot prove ownership by session.
func listedRuntimeObject(ctx context.Context, eng ReapEngine, id string, args ...string) (bool, error) {
	ids, err := engineLines(ctx, eng, args...)
	if err != nil {
		return false, err
	}
	for _, candidate := range ids {
		if sameRuntimeObjectID(candidate, id) {
			return true, nil
		}
	}
	return false, nil
}

func SweepManagedOrphans(ctx context.Context, eng ReapEngine, live map[string]bool) error {
	seen := map[string]bool{}
	reapIfOrphan := func(sessID string) error {
		sessID = strings.TrimSpace(sessID)
		if sessID == "" || sessID == "<no value>" || !strings.HasPrefix(sessID, "sess-") || live[sessID] || seen[sessID] {
			return nil
		}
		seen[sessID] = true
		return ReapBySession(ctx, eng, sessID)
	}

	ids, err := engineLines(ctx, eng, "ps", "-aq", "--filter", "label="+managedLabel)
	if err != nil {
		return fmt.Errorf("list managed containers: %w", err)
	}
	for _, id := range ids {
		sessID, err := engineOutput(ctx, eng, "inspect", "-f", `{{ index .Config.Labels "safeslop.session" }}`, id)
		if err != nil {
			stillPresent, verifyErr := listedRuntimeObject(ctx, eng, id, "ps", "-aq", "--filter", "label="+managedLabel)
			if verifyErr == nil && !stillPresent {
				continue
			}
			return fmt.Errorf("inspect managed container %s: %w", id, err)
		}
		if err := reapIfOrphan(sessID); err != nil {
			return err
		}
	}

	networks, err := engineLines(ctx, eng, "network", "ls", "-q", "--filter", "label="+managedLabel)
	if err != nil {
		return fmt.Errorf("list managed networks: %w", err)
	}
	for _, id := range networks {
		sessID, err := engineOutput(ctx, eng, "network", "inspect", "-f", `{{ index .Labels "safeslop.session" }}`, id)
		if err != nil {
			stillPresent, verifyErr := listedRuntimeObject(ctx, eng, id, "network", "ls", "-q", "--filter", "label="+managedLabel)
			if verifyErr == nil && !stillPresent {
				continue
			}
			return fmt.Errorf("inspect managed network %s: %w", id, err)
		}
		if err := reapIfOrphan(sessID); err != nil {
			return err
		}
	}
	return nil
}

// GCOptions controls image garbage collection. Keep preserves the N most-recent unprotected managed
// images after profile/lock/session references have been removed from consideration.
type GCOptions struct {
	Until string
	Keep  int
}

// GCProtection supplies the roots that protect images from GC: successfully resolving profiles,
// committed lockfiles, and live session records (specs/0058 N6).
type GCProtection struct {
	PolicyPaths []string
	LockPaths   []string
	SessionDir  string
}

type imageRecord struct {
	Ref       string
	CreatedAt string
}

// ReapEngine is the small command seam GC/reap need from a container engine. It is
// satisfied by runtime.Engine and kept local so tests can fake command execution.
type ReapEngine interface {
	Command(ctx context.Context, args ...string) *exec.Cmd
}

// GCImages removes unreferenced safeslop-managed images, never deleting images referenced by a current
// profile recipe, a safeslop.lock.json, or a live session. It returns the image refs it removed.
func GCImages(ctx context.Context, eng ReapEngine, opts GCOptions, protect GCProtection) ([]string, error) {
	protected, err := protectedImageRefs(protect)
	if err != nil {
		return nil, err
	}
	cutoff, hasCutoff, err := gcCutoff(time.Now(), opts.Until)
	if err != nil {
		return nil, err
	}
	args := []string{"image", "ls", "--format", "{{.Repository}}:{{.Tag}} {{.CreatedAt}}", "--filter", "label=" + managedLabel}
	lines, err := engineLines(ctx, eng, args...)
	if err != nil {
		return nil, fmt.Errorf("list managed images: %w", err)
	}
	images := parseImageRecords(lines)
	sort.SliceStable(images, func(i, j int) bool { return images[i].CreatedAt > images[j].CreatedAt })
	var candidates []imageRecord
	for _, img := range images {
		if protected[img.Ref] {
			continue
		}
		if hasCutoff {
			created, err := parseImageCreatedAt(img.CreatedAt)
			if err != nil || created.After(cutoff) {
				continue
			}
		}
		candidates = append(candidates, img)
	}
	if opts.Keep > 0 && len(candidates) > opts.Keep {
		candidates = candidates[opts.Keep:]
	} else if opts.Keep > 0 {
		candidates = nil
	}
	removed := make([]string, 0, len(candidates))
	for _, img := range candidates {
		if err := eng.Command(ctx, "image", "rm", img.Ref).Run(); err != nil {
			return removed, fmt.Errorf("remove image %s: %w", img.Ref, err)
		}
		removed = append(removed, img.Ref)
	}
	return removed, nil
}

func engineLines(ctx context.Context, eng ReapEngine, args ...string) ([]string, error) {
	out, err := engineOutput(ctx, eng, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	s := bufio.NewScanner(strings.NewReader(out))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func engineOutput(ctx context.Context, eng ReapEngine, args ...string) (string, error) {
	cmd := eng.Command(ctx, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

func parseImageRecords(lines []string) []imageRecord {
	out := make([]imageRecord, 0, len(lines))
	for _, line := range lines {
		ref, created, ok := strings.Cut(line, " ")
		if !ok || ref == "" || strings.HasSuffix(ref, ":<none>") {
			continue
		}
		out = append(out, imageRecord{Ref: ref, CreatedAt: strings.TrimSpace(created)})
	}
	return out
}

func gcCutoff(now time.Time, until string) (time.Time, bool, error) {
	until = strings.TrimSpace(until)
	if until == "" {
		return time.Time{}, false, nil
	}
	if d, err := time.ParseDuration(until); err == nil {
		return now.Add(-d), true, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, until); err == nil {
			return t, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("--until must be a Go duration like 24h or an RFC3339/date timestamp")
}

func parseImageCreatedAt(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05 -0700 MST", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse image CreatedAt %q", s)
}

func protectedImageRefs(p GCProtection) (map[string]bool, error) {
	protected := map[string]bool{}
	for _, path := range p.PolicyPaths {
		if path == "" {
			continue
		}
		cfg, err := policy.Load(path)
		if err != nil {
			continue // only successfully-resolving profiles anchor GC
		}
		for _, prof := range cfg.Profiles {
			if prof.Environment != "container" {
				continue
			}
			resolved, err := policy.Resolve(prof)
			if err != nil {
				continue
			}
			recipe, err := ResolveRecipe(resolved.IdentitySet)
			if err != nil {
				continue
			}
			protected[recipe.AgentImage] = true
			protected[recipe.BaseImage] = true
		}
	}
	for _, path := range p.LockPaths {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var lf struct {
			RecipeID string `json:"recipeID"`
		}
		if err := json.Unmarshal(b, &lf); err != nil || lf.RecipeID == "" {
			continue
		}
		protected[toolsImageRepo+":"+lf.RecipeID] = true
	}
	if p.SessionDir != "" {
		store := engsession.NewStore(p.SessionDir)
		sessions, err := store.List()
		if err != nil {
			return nil, fmt.Errorf("list sessions for gc protection: %w", err)
		}
		for _, sess := range sessions {
			if sess.Status != engsession.StatusRunning {
				continue
			}
			if sess.Image != "" {
				protected[sess.Image] = true
			}
			if sess.RecipeID != "" {
				protected[toolsImageRepo+":"+sess.RecipeID] = true
			}
		}
	}
	return protected, nil
}

func ParseKeep(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--keep must be a non-negative integer")
	}
	return n, nil
}

func DefaultProtection(policyPath, sessionDir string) GCProtection {
	var locks []string
	if policyPath != "" {
		locks = append(locks, filepath.Join(filepath.Dir(policyPath), "safeslop.lock.json"))
	}
	return GCProtection{PolicyPaths: []string{policyPath}, LockPaths: locks, SessionDir: sessionDir}
}

type LiveSessionIDs map[string]bool

// LegacySessionReapLabel reproduces the deployed layout-1 compose label without
// touching its stage directory. It exists only for upgrade-safe live sweeping.
func LegacySessionReapLabel(id, workspace string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspace))
	return fmt.Sprintf("%s-%08x", id, h.Sum32())
}

func LiveSessions(dir string) (LiveSessionIDs, error) {
	store := engsession.NewStore(dir)
	sessions, err := store.List()
	if err != nil {
		return nil, err
	}
	live := LiveSessionIDs{}
	for _, sess := range sessions {
		if sess.Status != engsession.StatusRunning {
			continue
		}
		// Keep the historical bare label plus the exact deployed layout label.
		// The former preserves compatibility with intermediate records/tests;
		// the latter prevents upgrade-time orphan sweep from reaping a live
		// hash-suffixed session.
		live[sess.ID] = true
		runtimeID, layout := sess.RuntimeIdentity()
		switch layout {
		case engsession.StageLayoutLegacy:
			live[LegacySessionReapLabel(sess.ID, sess.Workspace)] = true
		case engsession.StageLayoutSessionID:
			live[runtimeID] = true
		}
	}
	return live, nil
}

func SessionReapLabel(id string) string {
	return strings.TrimPrefix(id, "session-")
}

func SessionIDFromStageDir(stageDir string) string {
	return SessionReapLabel(filepath.Base(stageDir))
}
