//go:build darwin

package executor

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// memoryGuardSysctlTimeout bounds the sysctl call so a wedged exec can never delay
// a spawn. On timeout the guard reports "unknown" and stays out of the way.
const memoryGuardSysctlTimeout = 2 * time.Second

// systemFreeMemoryPct reads kern.memorystatus_level, the kernel's own
// percent-of-memory-still-free figure. It is what Activity Monitor's memory
// pressure display is derived from, so the number a user sees there and the number
// this guard acts on are the same one.
//
// Note this is deliberately NOT "free RAM" in the naive sense: macOS aggressively
// fills RAM with cache and compressed pages, so free-page counts read alarmingly low
// on a perfectly healthy machine. memorystatus_level already accounts for what the
// kernel can reclaim, which is why it's the right signal here.
func systemFreeMemoryPct() (pct int, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), memoryGuardSysctlTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sysctl", "-n", "kern.memorystatus_level").Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return n, true
}
