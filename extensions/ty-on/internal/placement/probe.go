package placement

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// HostStat is what a probe learned about one host.
type HostStat struct {
	// Reachable is false when the probe could not measure the host at all.
	Reachable bool
	// FreeKB is available memory in kilobytes. Only meaningful when Reachable.
	FreeKB int64
}

// Prober measures the fleet so two candidate hosts can be compared.
type Prober interface {
	// Probe returns a stat per host name, keyed the way the inventory keys it.
	Probe(ctx context.Context) (map[string]HostStat, error)
}

// OnProber shells out to the `on` CLI, which already knows how to probe the
// fleet in parallel. `on` is an optional dependency: if it is not installed we
// report that rather than reimplementing the probe.
type OnProber struct {
	// InventoryPath is passed through as ON_HOSTS so `on` reads exactly the
	// inventory this resolver read.
	InventoryPath string

	// Binary overrides the executable name. Empty means "on".
	Binary string
}

// Probe runs `on ls` and parses its table.
func (p OnProber) Probe(ctx context.Context) (map[string]HostStat, error) {
	bin := p.Binary
	if bin == "" {
		bin = "on"
	}

	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("the %s CLI is not installed", bin)
	}

	cmd := exec.CommandContext(ctx, path, "ls") //nolint:gosec // fixed argv, path from LookPath
	if p.InventoryPath != "" {
		cmd.Env = append(os.Environ(), "ON_HOSTS="+p.InventoryPath)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// A timeout surfaces as a kill here; the caller inspects ctx and turns
		// that into the "stays local" reason, so just report what we saw.
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s ls failed: %s", bin, msg)
		}
		return nil, fmt.Errorf("%s ls failed: %v", bin, err)
	}

	stats := ParseOnLS(stdout.String())
	if len(stats) == 0 {
		return nil, fmt.Errorf("%s ls reported no hosts", bin)
	}
	return stats, nil
}

// ParseOnLS reads the table `on ls` prints:
//
//	HOST       SSH        CORES    AVAIL    TOTAL    LOAD
//	ol-agents  ol-agents     16   26565M   31337M    0.12  84% free
//	rex        rex            -        -        -       -  ssh: ...
//
// A row whose AVAIL column is missing or unparseable (a dash, an SSH error) is
// recorded as unreachable rather than dropped, so callers can tell "the host is
// down" apart from "the host is not in the fleet".
func ParseOnLS(out string) map[string]HostStat {
	stats := make(map[string]HostStat)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] == "HOST" || !isCount(fields[2]) {
			// Not a host row: the header, a blank line, or a stray message.
			continue
		}
		name := fields[0]
		freeKB, ok := parseSize(fields[3])
		stats[name] = HostStat{Reachable: ok, FreeKB: freeKB}
	}
	return stats
}

// isCount reports whether s is the CORES cell of a host row: a core count, or
// a dash when the host could not be reached. Anything else means the line is
// not part of the table.
func isCount(s string) bool {
	if s == "-" {
		return true
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

// parseSize reads a size cell such as "8777M", "26.5G" or "-" into kilobytes.
func parseSize(s string) (int64, bool) {
	if s == "" || s == "-" {
		return 0, false
	}

	mult := int64(1)
	switch s[len(s)-1] {
	case 'K', 'k':
		s = s[:len(s)-1]
	case 'M', 'm':
		mult, s = 1<<10, s[:len(s)-1]
	case 'G', 'g':
		mult, s = 1<<20, s[:len(s)-1]
	case 'T', 't':
		mult, s = 1<<30, s[:len(s)-1]
	}

	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return int64(n * float64(mult)), true
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
