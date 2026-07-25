package executor

// Pure parsers for the Linux memory signals, deliberately kept free of build tags
// and of any file I/O so they can be unit-tested on any platform (including the
// macOS laptops where this is usually developed). The Linux-only file reading that
// feeds them lives in memoryguard_linux.go.

import (
	"strconv"
	"strings"
)

// parseMeminfoFreePct computes percent-of-memory-available from /proc/meminfo.
//
// MemAvailable is the right numerator: it is the kernel's own estimate of memory
// obtainable without swapping, already accounting for reclaimable page cache and
// slab. Using MemFree instead would report single-digit percentages on a healthy
// Linux box that is simply using its RAM for cache — and in block mode that would
// wedge an agent fleet for no reason.
//
// MemAvailable has been present since Linux 3.14 (2014). Older kernels fall back to
// MemFree + Buffers + Cached, the approximation MemAvailable itself replaced.
func parseMeminfoFreePct(data string) (pct int, ok bool) {
	var total, available, free, buffers, cached int64
	var haveAvailable bool

	for _, line := range strings.Split(data, "\n") {
		key, valueField, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(valueField)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = v
		case "MemAvailable":
			available, haveAvailable = v, true
		case "MemFree":
			free = v
		case "Buffers":
			buffers = v
		case "Cached": // must not match "SwapCached": strings.Cut keys are exact
			cached = v
		}
	}

	if total <= 0 {
		return 0, false
	}
	if !haveAvailable {
		available = free + buffers + cached
	}
	return clampPct(available * 100 / total), true
}

// parseCgroupFreePct computes percent-of-memory-available from cgroup v2 files, so
// a containerised agent server is measured against its own limit rather than the
// host's RAM. Returns ok=false when the cgroup is unlimited ("max"), which is the
// normal case on a bare VM — the caller then falls back to /proc/meminfo.
//
// memory.current counts page cache, which is reclaimable, so subtracting
// inactive_file from memory.stat is what keeps a cache-heavy container from looking
// permanently starved. Without that subtraction a long-running container would sit
// at ~0% "free" forever and, in block mode, stop spawning entirely.
func parseCgroupFreePct(maxRaw, currentRaw, statRaw string) (pct int, ok bool) {
	maxRaw = strings.TrimSpace(maxRaw)
	if maxRaw == "" || maxRaw == "max" {
		return 0, false // no limit set; not a meaningful denominator
	}
	limit, err := strconv.ParseInt(maxRaw, 10, 64)
	if err != nil || limit <= 0 {
		return 0, false
	}
	current, err := strconv.ParseInt(strings.TrimSpace(currentRaw), 10, 64)
	if err != nil || current < 0 {
		return 0, false
	}

	// Treat reclaimable file cache as available.
	var inactiveFile int64
	for _, line := range strings.Split(statRaw, "\n") {
		if name, value, found := strings.Cut(strings.TrimSpace(line), " "); found && name == "inactive_file" {
			if v, err := strconv.ParseInt(value, 10, 64); err == nil && v >= 0 {
				inactiveFile = v
			}
			break
		}
	}

	used := current - inactiveFile
	if used < 0 {
		used = 0
	}
	if used > limit {
		used = limit
	}
	return clampPct((limit - used) * 100 / limit), true
}

func clampPct(v int64) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(v)
}
