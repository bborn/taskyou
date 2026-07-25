package executor

import (
	"errors"
	"testing"
)

func TestMemoryGuardModeFromEnv(t *testing.T) {
	cases := map[string]memoryGuardMode{
		"":         memoryGuardWarn, // unset must never block
		"warn":     memoryGuardWarn,
		"nonsense": memoryGuardWarn, // unrecognised must never block
		"off":      memoryGuardOff,
		"0":        memoryGuardOff,
		"false":    memoryGuardOff,
		"disabled": memoryGuardOff,
		"block":    memoryGuardBlock,
		"BLOCK":    memoryGuardBlock,
		" block ":  memoryGuardBlock,
		"defer":    memoryGuardBlock,
		"enforce":  memoryGuardBlock,
	}
	for in, want := range cases {
		t.Setenv("TY_MEMORY_GUARD", in)
		if got := memoryGuardModeFromEnv(); got != want {
			t.Errorf("TY_MEMORY_GUARD=%q: got mode %d, want %d", in, got, want)
		}
	}
}

func TestMemoryGuardMinFreePct(t *testing.T) {
	cases := map[string]int{
		"":     defaultMemoryGuardMinFreePct,
		"35":   35,
		"0":    0,
		"100":  100,
		"-1":   defaultMemoryGuardMinFreePct, // out of range falls back
		"101":  defaultMemoryGuardMinFreePct,
		"junk": defaultMemoryGuardMinFreePct,
	}
	for in, want := range cases {
		t.Setenv("TY_MEMORY_GUARD_MIN_FREE_PCT", in)
		if got := memoryGuardMinFreePct(); got != want {
			t.Errorf("TY_MEMORY_GUARD_MIN_FREE_PCT=%q: got %d, want %d", in, got, want)
		}
	}
}

// Off mode must be inert regardless of how low the threshold makes the machine look.
func TestGuardMemoryForSpawnOffIsInert(t *testing.T) {
	t.Setenv("TY_MEMORY_GUARD", "off")
	t.Setenv("TY_MEMORY_GUARD_MIN_FREE_PCT", "100")
	note, err := guardMemoryForSpawn(1)
	if err != nil || note != "" {
		t.Fatalf("off mode must be inert, got note=%q err=%v", note, err)
	}
}

// The critical safety property: the default (unset) mode never returns an error, so
// no ty user hits a spawn wall they didn't opt into — even with the threshold pinned
// so high that the machine is guaranteed to be "under pressure".
func TestGuardMemoryForSpawnDefaultNeverBlocks(t *testing.T) {
	t.Setenv("TY_MEMORY_GUARD", "")
	t.Setenv("TY_MEMORY_GUARD_MIN_FREE_PCT", "100")
	_, err := guardMemoryForSpawn(1)
	if err != nil {
		t.Fatalf("default mode must never block, got err=%v", err)
	}
}

// Block mode with a 100% threshold blocks iff the pressure signal is readable.
// Skips rather than fails where the signal is unavailable (non-Darwin CI).
func TestGuardMemoryForSpawnBlockMode(t *testing.T) {
	if _, ok := systemFreeMemoryPct(); !ok {
		t.Skip("kern.memorystatus_level unavailable on this platform")
	}
	t.Setenv("TY_MEMORY_GUARD", "block")
	t.Setenv("TY_MEMORY_GUARD_MIN_FREE_PCT", "100")
	if _, err := guardMemoryForSpawn(42); !errors.Is(err, ErrMemoryPressure) {
		t.Fatalf("want ErrMemoryPressure, got %v", err)
	}

	// With a 0% threshold nothing can be below it, so block mode must allow the spawn.
	t.Setenv("TY_MEMORY_GUARD_MIN_FREE_PCT", "0")
	if note, err := guardMemoryForSpawn(42); err != nil || note != "" {
		t.Fatalf("threshold 0 must always allow, got note=%q err=%v", note, err)
	}
}

func TestSystemFreeMemoryPctInRange(t *testing.T) {
	pct, ok := systemFreeMemoryPct()
	if !ok {
		t.Skip("kern.memorystatus_level unavailable on this platform")
	}
	if pct < 0 || pct > 100 {
		t.Fatalf("free pct out of range: %d", pct)
	}
	t.Logf("kern.memorystatus_level = %d%% free", pct)
}
