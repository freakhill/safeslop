//go:build linux

package session

import (
	"fmt"
	"os"
	"strings"
)

func processStartTokenOS(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	text := string(b)
	end := strings.LastIndex(text, ") ")
	if end < 0 || end+2 >= len(text) {
		return "", false
	}
	fields := strings.Fields(text[end+2:])
	// fields[0] is stat field 3 (state), so field 22 (starttime) is index 19.
	if len(fields) <= 19 || fields[19] == "" {
		return "", false
	}
	bootID := "unknown-boot"
	if bb, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if trimmed := strings.TrimSpace(string(bb)); trimmed != "" {
			bootID = trimmed
		}
	}
	return "linux:" + bootID + ":" + fields[19], true
}
