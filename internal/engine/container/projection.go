package container

import (
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/hostpath"
	"github.com/freakhill/safeslop/internal/engine/policy"
)

const (
	projPresent           = "present"
	projSkippedAbsent     = "skipped-absent"
	projSkippedUnreadable = "skipped-unreadable"
	projSkippedNonRegular = "skipped-nonregular"
)

const (
	ProjectionTargetOutsideRoot = hostpath.ProjectionTargetOutsideRoot
	ProjectionTargetExcluded    = hostpath.ProjectionTargetExcluded
	ProjectionSymlinkLoop       = hostpath.ProjectionSymlinkLoop
	ProjectionUnsafeDescendant  = hostpath.ProjectionUnsafeDescendant
	ProjectionSourceType        = hostpath.ProjectionSourceType
	ProjectionSnapshotChanged   = hostpath.ProjectionSnapshotChanged
	ProjectionSafetyUnsupported = hostpath.ProjectionSafetyUnsupported
	ProjectionRequiredAbsent    = hostpath.ProjectionRequiredAbsent
)

type ProjectionError = hostpath.ProjectionError
type ProjectionMount = hostpath.ProjectionMount
type ProjectionManifest = hostpath.ProjectionManifest

func SnapshotProjection(home, stageDir string, proj policy.Projection) (ProjectionManifest, error) {
	return hostpath.SnapshotProjection(home, stageDir, proj)
}

func projectionBoundaryError() error {
	return hostpath.ProjectionBoundaryError()
}

func escapesRoot(rel string) bool {
	return rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
