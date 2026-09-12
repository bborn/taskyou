package ui

import (
	"os"
	osExec "os/exec"
	"strconv"
	"strings"
)

// terminalApps maps a fragment of $TERM_PROGRAM, or of the executable path of a
// process that owns our tty, to the macOS application name that opens it.
var terminalApps = []struct {
	match string // lowercase substring to look for
	app   string // macOS application name
}{
	{"iterm", "iTerm"},
	{"apple_terminal", "Terminal"},
	{"terminal.app", "Terminal"},
	{"ghostty", "Ghostty"},
	{"wezterm", "WezTerm"},
	{"kitty", "kitty"},
	{"alacritty", "Alacritty"},
	{"hyper", "Hyper"},
}

// matchTerminalApp returns the application name for a $TERM_PROGRAM value or an
// executable path, or "" when it names no terminal we recognise.
func matchTerminalApp(s string) string {
	s = strings.ToLower(s)
	for _, t := range terminalApps {
		if strings.Contains(s, t.match) {
			return t.app
		}
	}
	return ""
}

// detectTerminalApp returns the terminal application ty is displayed in.
// $TERM_PROGRAM answers this directly, except inside tmux, where it reads
// "tmux" and hides the real terminal; there we walk up from the tty of the tmux
// client showing our pane until we reach the process that owns it.
func detectTerminalApp() string {
	if app := matchTerminalApp(os.Getenv("TERM_PROGRAM")); app != "" {
		return app
	}
	if os.Getenv("TMUX") == "" {
		return ""
	}
	return terminalOwningTmuxClient()
}

// tmuxClientTTY returns the tty of the tmux client displaying our own pane.
// It asks about $TMUX_PANE rather than the "current" pane, which is racy when
// several clients are attached to the same session.
func tmuxClientTTY() string {
	args := []string{"display-message", "-p"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "#{client_tty}")
	out, err := osExec.Command("tmux", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// procInfo is one row of the process table: a parent pid and an executable path.
type procInfo struct {
	ppid int
	comm string
}

// parseProcessTable reads `ps -axo pid=,ppid=,comm=` output into pid -> parent
// and executable path. Executable paths contain spaces (macOS app bundles live
// under "Application Support"), so only the first two fields are split off.
func parseProcessTable(psOutput string) map[int]procInfo {
	table := make(map[int]procInfo)
	for _, line := range strings.Split(psOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidStr, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		ppidStr, comm, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(ppidStr)
		if err != nil {
			continue
		}
		table[pid] = procInfo{ppid: ppid, comm: strings.TrimLeft(comm, " ")}
	}
	return table
}

// parsePIDList reads a column of pids, one per line, as `ps -t <tty> -o pid=`
// prints them.
func parsePIDList(psOutput string) []int {
	var pids []int
	for _, line := range strings.Split(psOutput, "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// maxAncestorHops bounds the walk up the process tree so a cycle or a very deep
// chain cannot spin.
const maxAncestorHops = 12

// terminalInAncestry walks each starting pid up through its parents and returns
// the first terminal application named along the way.
func terminalInAncestry(pids []int, table map[int]procInfo) string {
	for _, start := range pids {
		pid := start
		for hop := 0; hop < maxAncestorHops; hop++ {
			info, ok := table[pid]
			if !ok {
				break
			}
			if app := matchTerminalApp(info.comm); app != "" {
				return app
			}
			if info.ppid <= 1 {
				break
			}
			pid = info.ppid
		}
	}
	return ""
}

// terminalOwningTmuxClient identifies the terminal application that is drawing
// the tmux client we are displayed in. Our own parent chain is useless here: it
// leads to the tmux server, which is detached from every terminal.
func terminalOwningTmuxClient() string {
	tty := tmuxClientTTY()
	if tty == "" {
		return ""
	}
	ttyOut, err := osExec.Command("ps", "-t", tty, "-o", "pid=").Output()
	if err != nil {
		return ""
	}
	pids := parsePIDList(string(ttyOut))
	if len(pids) == 0 {
		return ""
	}
	psOut, err := osExec.Command("ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return ""
	}
	return terminalInAncestry(pids, parseProcessTable(string(psOut)))
}

// shellSingleQuote wraps s so a POSIX shell reads it as one literal word.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptQuote renders s as an AppleScript string literal.
func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// iTermNewTabScript opens a tab in iTerm2's front window and changes to workdir.
// The tab keeps the user's default profile, so it runs whatever login shell they
// normally get; we only send it a cd. iTerm2 selects a tab as it creates it, so
// the user lands in the new tab. With no window open there is nothing to add a
// tab to, so we create a window instead.
func iTermNewTabScript(workdir string) string {
	cd := appleScriptQuote("cd " + shellSingleQuote(workdir))
	return `tell application "iTerm2"
  activate
  set w to current window
  if w is missing value then
    set w to (create window with default profile)
    tell current session of w to write text ` + cd + `
  else
    tell w
      create tab with default profile
      tell current session to write text ` + cd + `
    end tell
  end if
end tell`
}

// appleTerminalNewWindowScript opens Terminal.app at workdir. Terminal.app has
// no scriptable "new tab", so this is a window.
func appleTerminalNewWindowScript(workdir string) string {
	cd := appleScriptQuote("cd " + shellSingleQuote(workdir))
	return `tell application "Terminal"
  activate
  do script ` + cd + `
end tell`
}

// buildTerminalCommand returns the command that opens a new terminal at workdir
// in the given application. iTerm2 and Terminal.app are scripted directly so the
// new surface is a tab in the window the user is already looking at; anything
// else is handed to LaunchServices, which opens it at that directory.
func buildTerminalCommand(app, workdir string) *osExec.Cmd {
	switch app {
	case "iTerm":
		return osExec.Command("osascript", "-e", iTermNewTabScript(workdir))
	case "Terminal":
		return osExec.Command("osascript", "-e", appleTerminalNewWindowScript(workdir))
	case "":
		// Nothing recognised, so fall back to the terminal every Mac has.
		return osExec.Command("open", "-a", "Terminal", workdir)
	default:
		return osExec.Command("open", "-a", app, workdir)
	}
}
