package reaper

import (
	"fmt"
	"os"
	osexec "os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// psFormat is the process-table view the reaper needs: identity, parentage,
// whether a terminal is attached, how old it is, and — the part that actually
// matters — the full command line, which is where the worktree path lives.
const psFormat = "pid=,ppid=,tty=,etime=,command="

// ScanProcesses returns the current process table.
func ScanProcesses() ([]Process, error) {
	out, err := osexec.Command("ps", "-Ao", psFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return ParsePS(string(out)), nil
}

// ParsePS parses `ps -Ao pid=,ppid=,tty=,etime=,command=` output. Rows that
// don't parse are skipped rather than failing the sweep — a malformed line must
// never be able to stop the reaper, and must never be interpreted as a kill.
func ParsePS(out string) []Process {
	var procs []Process
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields, rest := splitN(line, 4)
		if len(fields) < 4 || rest == "" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		age, ok := ParseElapsed(fields[3])
		if !ok {
			continue
		}
		procs = append(procs, Process{PID: pid, PPID: ppid, TTY: fields[2], Age: age, Command: rest})
	}
	return procs
}

// splitN pulls n whitespace-separated tokens off the front of a line and
// returns them plus the untouched remainder (which may itself contain spaces).
func splitN(line string, n int) ([]string, string) {
	var tokens []string
	rest := line
	for i := 0; i < n; i++ {
		rest = strings.TrimLeft(rest, " \t")
		idx := strings.IndexAny(rest, " \t")
		if idx < 0 {
			if rest != "" {
				tokens = append(tokens, rest)
			}
			return tokens, ""
		}
		tokens = append(tokens, rest[:idx])
		rest = rest[idx:]
	}
	return tokens, strings.TrimLeft(rest, " \t")
}

// ParseElapsed parses ps's etime column: [[dd-]hh:]mm:ss.
func ParseElapsed(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var days int
	if i := strings.Index(s, "-"); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, false
		}
		days = d
		s = s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0, false
		}
		nums = append(nums, n)
	}
	var hours, mins, secs int
	if len(nums) == 3 {
		hours, mins, secs = nums[0], nums[1], nums[2]
	} else {
		mins, secs = nums[0], nums[1]
	}
	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(mins)*time.Minute +
		time.Duration(secs)*time.Second, true
}

// LivePanePIDs returns the PID of every process tmux currently owns a pane for,
// across all sessions. Anything descended from one of these is still in a pane
// and therefore not an orphan.
func LivePanePIDs() map[int]bool {
	pids := make(map[int]bool)
	out, err := osexec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid}").Output()
	if err != nil {
		return pids
	}
	for _, line := range strings.Split(string(out), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
			pids[pid] = true
		}
	}
	return pids
}

// ProtectedPIDs returns the sweeping process and every ancestor of it. `ty
// sessions cleanup` is frequently run by an agent from inside its own task
// worktree, where its whole process chain matches that worktree's path; without
// this the sweep would be able to kill the thing running it.
func ProtectedPIDs(procs []Process) map[int]bool {
	parents := make(map[int]int, len(procs))
	for _, p := range procs {
		parents[p.PID] = p.PPID
	}
	protected := map[int]bool{}
	for cur := os.Getpid(); cur > 1 && !protected[cur]; {
		protected[cur] = true
		next, ok := parents[cur]
		if !ok {
			break
		}
		cur = next
	}
	return protected
}

// Signaller sends a signal to a PID. Swappable so tests can observe the kill
// sequence without touching real processes.
type Signaller func(pid int, sig syscall.Signal) error

// SystemSignaller delivers signals for real.
func SystemSignaller(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

// KillResult records what happened to one reaped process.
type KillResult struct {
	Decision Decision
	Signal   string // "TERM", "KILL", or "" when nothing was sent
	Err      error
}

// Reap SIGTERMs everything marked for reaping, waits out the grace period, and
// SIGKILLs whatever is still alive. Decisions that are not marked Reap are
// ignored, so the caller can hand it the full plan.
func Reap(decisions []Decision, grace time.Duration, sig Signaller) []KillResult {
	if sig == nil {
		sig = SystemSignaller
	}

	var order []int
	results := map[int]*KillResult{}
	var pending []int

	for _, d := range decisions {
		if !d.Reap {
			continue
		}
		r := &KillResult{Decision: d, Signal: "TERM"}
		if err := sig(d.Process.PID, syscall.SIGTERM); err != nil {
			r.Err = err
		} else {
			pending = append(pending, d.Process.PID)
		}
		results[d.Process.PID] = r
		order = append(order, d.Process.PID)
	}

	// Give the polite signal a chance to land before escalating. Signal 0 is
	// the liveness probe: it delivers nothing but fails once the PID is gone.
	deadline := time.Now().Add(grace)
	for len(pending) > 0 {
		var alive []int
		for _, pid := range pending {
			if sig(pid, syscall.Signal(0)) == nil {
				alive = append(alive, pid)
			}
		}
		pending = alive
		if len(pending) == 0 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	for _, pid := range pending {
		r := results[pid]
		r.Signal = "KILL"
		r.Err = sig(pid, syscall.SIGKILL)
	}

	out := make([]KillResult, 0, len(order))
	for _, pid := range order {
		out = append(out, *results[pid])
	}
	return out
}
