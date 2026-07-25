package executor

import "testing"

// Real shape of /proc/meminfo, trimmed. 32GB box with most of RAM in cache.
const sampleMeminfo = `MemTotal:       32819104 kB
MemFree:          412300 kB
MemAvailable:   24117248 kB
Buffers:          204800 kB
Cached:         23068672 kB
SwapCached:        12345 kB
Active:          6291456 kB
SwapTotal:       2097148 kB
SwapFree:        2097148 kB
`

func TestParseMeminfoFreePct(t *testing.T) {
	pct, ok := parseMeminfoFreePct(sampleMeminfo)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	// 24117248 / 32819104 = 73%
	if pct != 73 {
		t.Fatalf("got %d%%, want 73%%", pct)
	}
}

// The whole point of preferring MemAvailable: MemFree alone would read 1% here and,
// in block mode, wedge a perfectly healthy box that is merely using RAM for cache.
func TestParseMeminfoPrefersAvailableOverFree(t *testing.T) {
	pct, _ := parseMeminfoFreePct(sampleMeminfo)
	if pct < 50 {
		t.Fatalf("cache-heavy but healthy box reported %d%% free; MemAvailable is not being used", pct)
	}
}

// Pre-3.14 kernels have no MemAvailable; fall back to MemFree+Buffers+Cached.
func TestParseMeminfoFallbackWithoutMemAvailable(t *testing.T) {
	old := `MemTotal:       1000000 kB
MemFree:          100000 kB
Buffers:           50000 kB
Cached:           350000 kB
`
	pct, ok := parseMeminfoFreePct(old)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if pct != 50 { // (100000+50000+350000)/1000000
		t.Fatalf("got %d%%, want 50%%", pct)
	}
}

// "Cached" must not be satisfied by "SwapCached" — an exact key match, not a prefix.
func TestParseMeminfoDoesNotConfuseSwapCached(t *testing.T) {
	in := `MemTotal:       1000000 kB
MemFree:          100000 kB
Buffers:                0 kB
SwapCached:       900000 kB
Cached:           100000 kB
`
	pct, ok := parseMeminfoFreePct(in)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if pct != 20 { // (100000+0+100000)/1000000 — SwapCached must be ignored
		t.Fatalf("got %d%%, want 20%%; SwapCached leaked into Cached", pct)
	}
}

func TestParseMeminfoRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "not meminfo at all", "MemTotal:       0 kB\n"} {
		if _, ok := parseMeminfoFreePct(in); ok {
			t.Errorf("expected failure for %q", in)
		}
	}
}

func TestParseCgroupFreePct(t *testing.T) {
	// 2GB limit, 1GB charged, none of it reclaimable cache -> 50% free.
	pct, ok := parseCgroupFreePct("2147483648", "1073741824", "anon 1073741824\ninactive_file 0\n")
	if !ok || pct != 50 {
		t.Fatalf("got %d%% ok=%v, want 50%% ok=true", pct, ok)
	}
}

// Page cache is reclaimable: a container whose charge is almost all cache is NOT
// under pressure. Without the inactive_file subtraction this reads 0% free and
// block mode would stop spawning permanently.
func TestParseCgroupTreatsPageCacheAsAvailable(t *testing.T) {
	pct, ok := parseCgroupFreePct("2147483648", "2147483648", "anon 107374182\ninactive_file 2040109466\n")
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if pct < 90 {
		t.Fatalf("cache-heavy container reported %d%% free; inactive_file not subtracted", pct)
	}
}

// An unlimited cgroup is not a meaningful denominator; caller must fall back.
func TestParseCgroupUnlimitedFallsBack(t *testing.T) {
	for _, raw := range []string{"max", "max\n", "", "   "} {
		if _, ok := parseCgroupFreePct(raw, "1073741824", ""); ok {
			t.Errorf("expected ok=false for memory.max=%q", raw)
		}
	}
}

func TestParseCgroupRejectsGarbage(t *testing.T) {
	cases := [][2]string{
		{"notanumber", "123"},
		{"0", "123"},
		{"-1", "123"},
		{"2147483648", "notanumber"},
		{"2147483648", "-5"},
	}
	for _, c := range cases {
		if _, ok := parseCgroupFreePct(c[0], c[1], ""); ok {
			t.Errorf("expected ok=false for max=%q current=%q", c[0], c[1])
		}
	}
}

// Usage above the limit (transiently possible) must clamp to 0, never go negative.
func TestParseCgroupClampsOverLimit(t *testing.T) {
	pct, ok := parseCgroupFreePct("1000", "5000", "")
	if !ok || pct != 0 {
		t.Fatalf("got %d%% ok=%v, want 0%% ok=true", pct, ok)
	}
}

func TestClampPct(t *testing.T) {
	cases := map[int64]int{-5: 0, 0: 0, 42: 42, 100: 100, 101: 100, 1 << 40: 100}
	for in, want := range cases {
		if got := clampPct(in); got != want {
			t.Errorf("clampPct(%d) = %d, want %d", in, got, want)
		}
	}
}
