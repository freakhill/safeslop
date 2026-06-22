package uninstall

import (
	"os/exec"
	"strconv"
	"strings"
)

// runningInstances best-effort reports PIDs of processes executing binPath, via `pgrep -f`. It is used
// only to WARN before a Path A removal (e.g. a running `tart` still holding VMs) — never to block. No
// Path A tool installs a launchd plist today; if one ever does, a `launchctl bootout` of that
// safeslop-owned label would belong here, before the file removal. A probe failure yields no pids (the
// warning is advisory, so a missing pgrep must not derail an uninstall).
func runningInstances(binPath string) []int {
	out, err := exec.Command("pgrep", "-f", binPath).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Fields(strings.TrimSpace(string(out))) {
		if pid, perr := strconv.Atoi(line); perr == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
