package executor

// Memory admission guard for executor spawns.
//
// Why this exists: nothing in ty bounded how many agent sessions could be live at
// once. Each session is a claude process plus its MCP servers (~100-150MB resident,
// considerably more in footprint once macOS compresses the cold pages), so a queue
// that fans out freely can walk a workstation into swap thrashing — at which point
// every session, dev server and test run on the box gets slower, including the ones
// already doing useful work.
//
// Deliberately NOT a concurrency cap. A fixed "max N sessions" is a hidden wall:
// the right number depends entirely on how much RAM the machine has and what else
// is running, and a user who legitimately wants dozens of concurrent agents should
// not be told "no" by an arbitrary constant. So this gates on the machine's ACTUAL
// memory pressure instead. With headroom, spawn as many as you like; the guard only
// has an opinion once the kernel says memory is genuinely short.
//
// Default mode is "warn": log loudly, never block. That keeps behaviour unchanged
// for everyone else while making the condition visible. Set TY_MEMORY_GUARD=block
// to have spawns deferred under pressure instead.
//
//	TY_MEMORY_GUARD=off|warn|block        (default: warn)
//	TY_MEMORY_GUARD_MIN_FREE_PCT=<0-100>  (default: 20)
//
// The signal is Darwin's kern.memorystatus_level — the same free-memory percentage
// Activity Monitor's pressure graph is derived from. Activity Monitor's zones are
// green 100-50, yellow 50-30, red 30-0, so the default threshold of 20 is well into
// the red: this fires when the machine is already hurting, not merely busy. On any
// platform where the signal is unavailable the guard is inert.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type memoryGuardMode int

const (
	memoryGuardOff memoryGuardMode = iota
	memoryGuardWarn
	memoryGuardBlock
)

// defaultMemoryGuardMinFreePct is the free-memory percentage below which the guard
// considers the machine to be under real pressure. 20 sits inside Activity Monitor's
// red zone (30-0).
const defaultMemoryGuardMinFreePct = 20

// memoryGuardSysctlTimeout bounds the sysctl call so a wedged exec can never delay
// a spawn. On timeout the guard reports "unknown" and stays out of the way.
const memoryGuardSysctlTimeout = 2 * time.Second

// ErrMemoryPressure is returned by guardMemoryForSpawn in block mode when the
// machine is below the free-memory threshold. Callers should treat it as "try this
// task again shortly", not as a task failure.
var ErrMemoryPressure = errors.New("executor: deferring spawn, system memory pressure")

func memoryGuardModeFromEnv() memoryGuardMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TY_MEMORY_GUARD"))) {
	case "off", "0", "false", "disabled":
		return memoryGuardOff
	case "block", "defer", "enforce":
		return memoryGuardBlock
	default:
		// Unset or unrecognised: warn. Never silently block.
		return memoryGuardWarn
	}
}

func memoryGuardMinFreePct() int {
	raw := strings.TrimSpace(os.Getenv("TY_MEMORY_GUARD_MIN_FREE_PCT"))
	if raw == "" {
		return defaultMemoryGuardMinFreePct
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 100 {
		return defaultMemoryGuardMinFreePct
	}
	return n
}

// systemFreeMemoryPct returns the kernel's free-memory percentage, or ok=false when
// the signal isn't available (non-Darwin, sysctl missing, unparseable, timeout).
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

// guardMemoryForSpawn consults system memory pressure before an executor spawn.
//
// It returns a human-readable note whenever the machine is under pressure (empty
// string otherwise) so the caller can surface it with whatever logger it has, and a
// non-nil error ONLY in block mode. Callers must always spawn when err == nil, even
// if note is non-empty — that is the whole point of the default warn mode.
func guardMemoryForSpawn(taskID int64) (note string, err error) {
	mode := memoryGuardModeFromEnv()
	if mode == memoryGuardOff {
		return "", nil
	}

	freePct, ok := systemFreeMemoryPct()
	if !ok {
		return "", nil // no signal, no opinion
	}
	minFree := memoryGuardMinFreePct()
	if freePct >= minFree {
		return "", nil
	}

	if mode == memoryGuardBlock {
		return "", fmt.Errorf("%w: %d%% memory free (threshold %d%%), task %d — set TY_MEMORY_GUARD=off or lower TY_MEMORY_GUARD_MIN_FREE_PCT to spawn anyway",
			ErrMemoryPressure, freePct, minFree, taskID)
	}
	return fmt.Sprintf("system memory is low (%d%% free, threshold %d%%): spawning anyway; set TY_MEMORY_GUARD=block to defer spawns under pressure",
		freePct, minFree), nil
}
