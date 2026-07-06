//go:build !linux && !darwin

package session

func processStartTokenOS(pid int) (string, bool) { return "", false }
