//go:build linux

package executor

import "os"

// cgroup v2 paths. Inside a container the container's own cgroup is mounted at the
// hierarchy root, so these read that container's limit. On a bare host the root
// cgroup has no memory.max and the read simply fails, falling through to
// /proc/meminfo — which is what we want there anyway.
const (
	cgroupV2MemoryMax     = "/sys/fs/cgroup/memory.max"
	cgroupV2MemoryCurrent = "/sys/fs/cgroup/memory.current"
	cgroupV2MemoryStat    = "/sys/fs/cgroup/memory.stat"
	procMeminfo           = "/proc/meminfo"
)

// systemFreeMemoryPct returns percent-of-memory-available on Linux.
//
// cgroup v2 is consulted first so a containerised agent server is measured against
// its own memory limit rather than the host's total RAM — otherwise a 2GB container
// on a 64GB host would look like it had endless headroom right up until the OOM
// killer fired. Falls back to /proc/meminfo when there is no cgroup limit.
//
// Deliberately not using PSI (/proc/pressure/memory): it is the better *pressure*
// signal, but it reports stall time rather than a fraction of memory, so it can't
// share TY_MEMORY_GUARD_MIN_FREE_PCT's meaning with the macOS path. Keeping one
// threshold that means the same thing on every platform is worth more here than a
// slightly sharper Linux signal.
func systemFreeMemoryPct() (pct int, ok bool) {
	maxRaw, errMax := os.ReadFile(cgroupV2MemoryMax)
	currentRaw, errCur := os.ReadFile(cgroupV2MemoryCurrent)
	if errMax == nil && errCur == nil {
		statRaw, _ := os.ReadFile(cgroupV2MemoryStat) // optional; absent just means no cache adjustment
		if pct, ok := parseCgroupFreePct(string(maxRaw), string(currentRaw), string(statRaw)); ok {
			return pct, true
		}
	}

	data, err := os.ReadFile(procMeminfo)
	if err != nil {
		return 0, false
	}
	return parseMeminfoFreePct(string(data))
}
