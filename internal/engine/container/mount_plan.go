package container

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
)

type composeMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

type composeMountPlan struct {
	Proxy []composeMount
	Agent []composeMount
}

func buildComposeMountPlan(p composeParams, requireSources bool) (composeMountPlan, error) {
	if err := validateProjectionSnapshotMounts(p.StageDir, p.Projection); err != nil {
		return composeMountPlan{}, err
	}
	workspacePath, stagePath, runtimePath := p.Workspace, p.StageDir, p.RuntimeDir
	if requireSources {
		var err error
		workspacePath, err = workspaceboundary.ResolveAbsolute(workspacePath)
		if err != nil {
			return composeMountPlan{}, fmt.Errorf("workspace boundary: %w", err)
		}
		stagePath, err = workspaceboundary.ResolveAbsolute(stagePath)
		if err != nil {
			return composeMountPlan{}, fmt.Errorf("runtime stage boundary: %w", err)
		}
		if err := workspaceboundary.RequireDisjoint(workspacePath, stagePath); err != nil {
			return composeMountPlan{}, err
		}
		runtimePath, err = workspaceboundary.ResolveAbsolute(runtimePath)
		if err != nil || runtimePath != stagePath {
			return composeMountPlan{}, errors.New("runtime source and private stage differ")
		}
	}
	plan := composeMountPlan{
		Proxy: []composeMount{
			{Source: filepath.Join(runtimePath, "squid.conf"), Target: "/etc/squid/squid.conf", ReadOnly: true},
			{Source: filepath.Join(runtimePath, "allowlist.domains"), Target: "/etc/squid/allowlist.domains", ReadOnly: true},
			{Source: filepath.Join(runtimePath, "session-grants.conf"), Target: "/etc/squid/session-grants.conf", ReadOnly: true},
		},
		Agent: []composeMount{
			{Source: workspacePath, Target: "/workspace", ReadOnly: false},
			{Source: stagePath, Target: "/safeslop/runtime", ReadOnly: true},
		},
	}
	if p.Projection != nil {
		for _, projected := range p.Projection.PresentMounts() {
			plan.Agent = append(plan.Agent, composeMount{Source: projected.Host, Target: projected.Container, ReadOnly: true})
		}
	}
	if err := validateServiceMounts("proxy", plan.Proxy, requireSources); err != nil {
		return composeMountPlan{}, err
	}
	if err := validateServiceMounts("agent", plan.Agent, requireSources); err != nil {
		return composeMountPlan{}, err
	}
	writable := 0
	for _, mount := range plan.Agent {
		if mount.ReadOnly {
			continue
		}
		writable++
		if mount.Source != workspacePath || mount.Target != "/workspace" {
			return composeMountPlan{}, errors.New("invalid writable bind mount")
		}
	}
	if writable != 1 {
		return composeMountPlan{}, errors.New("compose mount plan must contain exactly one writable workspace bind")
	}
	return plan, nil
}

func validateServiceMounts(service string, mounts []composeMount, requireSources bool) error {
	targets := make(map[string]struct{}, len(mounts))
	for _, mount := range mounts {
		if !filepath.IsAbs(mount.Source) || !filepath.IsAbs(mount.Target) || filepath.Clean(mount.Target) != mount.Target {
			return fmt.Errorf("%s bind mount is not absolute and clean", service)
		}
		if _, err := workspaceboundary.Candidate(mount.Source, "", string(filepath.Separator)); err != nil {
			return fmt.Errorf("%s bind source: %w", service, err)
		}
		if _, err := workspaceboundary.Candidate(mount.Target, "", string(filepath.Separator)); err != nil {
			return fmt.Errorf("%s bind target: %w", service, err)
		}
		if _, exists := targets[mount.Target]; exists {
			return fmt.Errorf("%s has duplicate bind target", service)
		}
		targets[mount.Target] = struct{}{}
		if !requireSources {
			continue
		}
		info, err := os.Lstat(mount.Source)
		if err != nil {
			return fmt.Errorf("%s bind source is unavailable", service)
		}
		if info.Mode()&os.ModeSymlink != 0 || !(info.Mode().IsRegular() || info.IsDir()) {
			return fmt.Errorf("%s bind source has an unsupported type", service)
		}
	}
	return nil
}
