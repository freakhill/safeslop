//go:build !darwin && !linux

package container

import "os"

func projectionSafetySupported() bool     { return false }
func fileMountID(*os.File) (uint64, bool) { return 0, false }
