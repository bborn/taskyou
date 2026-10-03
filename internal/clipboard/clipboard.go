// Package clipboard puts text on the user's clipboard from ty's side of the
// connection, not the agent's.
//
// An agent that wants to hand the user a command, a URL or a token cannot rely
// on the pane it runs in. Selecting text in a terminal picks up a hard line
// break wherever the agent's UI wrapped it, and an OSC 52 the agent emits has to
// survive every tmux and ssh hop between it and the user's terminal — for a task
// placed on another machine, that is several. So the agent calls
// taskyou_copy_to_clipboard, the call arrives here over ty's own channel (the
// MCP relay, for a placed task), and the text is delivered on the machine the
// task was scheduled from, byte for byte.
//
// Two routes, both tried:
//
//   - the machine's clipboard command (pbcopy, wl-copy, xclip, xsel, clip.exe),
//     for a ty running on the desktop the user sits at;
//   - OSC 52 through the agent tmux server's attached clients — ty's own views —
//     which ClipboardRelayArgs relays out to the user's terminal. That covers a
//     TUI reached over ssh, where the machine's clipboard is not the user's.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/tmuxctl"
)

// MaxBytes bounds what one copy may carry. A clipboard is for a command or a
// snippet; anything bigger belongs in a file or an artifact.
const MaxBytes = 1 << 20

// BufferName is the tmux paste buffer the OSC 52 route writes. A fixed name,
// so each copy replaces the last instead of piling up copies — which may be
// secrets — in the server's buffer list.
const BufferName = "ty-clipboard"

// Copier puts text on the user's clipboard.
type Copier struct {
	// GOOS picks the clipboard commands to try.
	GOOS string
	// Getenv reads the environment (WAYLAND_DISPLAY, DISPLAY, WSL_DISTRO_NAME).
	Getenv func(string) string
	// LookPath finds a clipboard command.
	LookPath func(string) (string, error)
	// Run runs name with args and stdin as its input.
	Run func(ctx context.Context, stdin string, name string, args ...string) error
	// TmuxClients reports how many clients are attached to the agent server.
	TmuxClients func(ctx context.Context) int
	// TmuxLoad loads text into the agent server's paste buffer and sends it to
	// its clients with OSC 52.
	TmuxLoad func(ctx context.Context, text string) error
}

// Default is the Copier for this process.
func Default() *Copier {
	return &Copier{
		GOOS:     runtime.GOOS,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, stdin, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdin = strings.NewReader(stdin)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		TmuxClients: func(ctx context.Context) int {
			out, err := tmuxctl.Agent(ctx, "list-clients", "-F", "#{client_name}").Output()
			if err != nil {
				return 0
			}
			return len(strings.Fields(string(out)))
		},
		TmuxLoad: func(ctx context.Context, text string) error {
			cmd := tmuxctl.Agent(ctx, "load-buffer", "-b", BufferName, "-w", "-")
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		},
	}
}

// Copy puts text on the clipboard with the default Copier.
func Copy(ctx context.Context, text string) ([]string, error) {
	return Default().Copy(ctx, text)
}

// command is one clipboard program and the arguments that make it read stdin.
type command struct {
	name string
	args []string
}

// systemCommands are the clipboard programs worth trying here, best first.
func (c *Copier) systemCommands() []command {
	switch c.GOOS {
	case "darwin":
		return []command{{"pbcopy", nil}}
	case "windows":
		return []command{{"clip.exe", nil}}
	}
	var cmds []command
	if c.Getenv("WAYLAND_DISPLAY") != "" {
		cmds = append(cmds, command{"wl-copy", nil})
	}
	if c.Getenv("DISPLAY") != "" {
		cmds = append(cmds,
			command{"xclip", []string{"-selection", "clipboard"}},
			command{"xsel", []string{"--clipboard", "--input"}})
	}
	if c.Getenv("WSL_DISTRO_NAME") != "" {
		cmds = append(cmds, command{"clip.exe", nil})
	}
	return cmds
}

// Copy delivers text by every route that is available and reports, in words
// fit to show the agent, where it went. It fails only when no route took it.
func (c *Copier) Copy(ctx context.Context, text string) ([]string, error) {
	if text == "" {
		return nil, errors.New("nothing to copy: text is empty")
	}
	if len(text) > MaxBytes {
		return nil, fmt.Errorf("text is %d bytes; the clipboard takes at most %d", len(text), MaxBytes)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var delivered, failures []string
	for _, cmd := range c.systemCommands() {
		if _, err := c.LookPath(cmd.name); err != nil {
			continue
		}
		if err := c.Run(ctx, text, cmd.name, cmd.args...); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", cmd.name, err))
			continue
		}
		delivered = append(delivered, "system clipboard ("+cmd.name+")")
		break
	}

	if c.TmuxClients(ctx) > 0 {
		if err := c.TmuxLoad(ctx, text); err != nil {
			failures = append(failures, fmt.Sprintf("tmux: %v", err))
		} else {
			delivered = append(delivered, "the terminal showing ty (OSC 52)")
		}
	}

	if len(delivered) == 0 {
		why := "no clipboard command (pbcopy, wl-copy, xclip, xsel, clip.exe) is usable here and no ty view is open to relay it"
		if len(failures) > 0 {
			why = strings.Join(failures, "; ")
		}
		return nil, errors.New("could not reach the clipboard: " + why)
	}
	return delivered, nil
}
