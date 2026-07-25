//go:build !darwin && !linux

package executor

// systemFreeMemoryPct has no implementation on this platform, so the guard is
// inert: guardMemoryForSpawn returns "no opinion" and spawns proceed exactly as
// they did before the guard existed. Failing open is the correct default — a guard
// that can't measure anything must never be the reason a task doesn't start.
func systemFreeMemoryPct() (pct int, ok bool) {
	return 0, false
}
