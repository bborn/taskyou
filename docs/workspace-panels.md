# Task workspace

The GUI keeps the task conversation and agent on the left and a workspace on
the right. Shell is the first tab. Use **+** to open a pull request summary,
browse files, or enter a relative file path. Markdown files render as documents;
other text files open in a read-only preview.

Tabs belong to the task and are shared between interfaces. Opening the same
resource again selects its existing tab. Closing a tab removes the view and
keeps the underlying shell session running. The GUI remembers its selected tab
and divider width locally. On a phone, **Show workspace** switches from the
conversation to the workspace without squeezing both into one row.

## Terminal UI

In task detail, press **w** to open the workspace beside the agent. It initially
shows Shell; **\\** closes the workspace and restores the ordinary shell view.
The agent and shell processes stay in their daemon session.

| Key | Action |
| --- | --- |
| Alt+t | Open the launcher |
| Up / Down, Enter | Choose a launcher action or file |
| Alt+Left / Alt+Right | Switch tabs |
| Alt+w | Close the active tab |
| Alt+r | Refresh content |
| Alt+h | Toggle context-aware shortcut help |
| Page Up / Page Down | Page through launcher actions or files |
| / in Files | Fuzzy-filter filenames; Enter applies, Enter again opens |
| Esc in Files | Cancel or clear the filter |
| Backspace in Files | Open the parent directory |
| Shift+arrows | Move between the surrounding tmux panes |

The launcher and file browser use Bubbles lists for selection and pagination.
Launcher search fuzzy-matches actions while retaining an explicit file-path
option. Shortcut help is generated from the same bindings that handle input.

When Shell is selected, typing goes to the shell, including Ctrl+C. Use Alt+t
to leave the shell for the launcher. File paths are relative to the task's
worktree, or to its registered project when no worktree exists.

## CLI and API

```sh
ty panel providers 42
ty panel list 42
ty panel open 42 pr
ty panel open 42 files
ty panel open 42 file docs/README.md
ty panel content 42 PANEL_ID
ty panel close 42 PANEL_ID
ty panel view 42
```

The HTTP equivalents are under `/api/tasks/{id}/panel-providers` and
`/api/tasks/{id}/panels`. POST a `provider_id` and optional `resource` to open a
tab; GET `/{panelID}/content` reads it; DELETE `/{panelID}` closes it.

## Providers and limits

Providers register metadata, canonical resource validation, and a content
loader in `internal/panel`. Both renderers consume shared content kinds:
`shell`, `markdown`, `text`, and `files`. New kinds need both TUI and GUI
renderers; the parity suite checks the declared contract. This first version
ships compiled providers. Runtime third-party plugin loading, browser embedding,
and code review agents are future additions.

PR summaries use TaskYou's cached GitHub status, including aggregate checks;
refresh reads the latest cache rather than starting a GitHub request. Text
previews are limited to 256 KiB and directory listings to 500 entries. Local
reads are confined using Go's `os.Root`. Remote previews use the task's recorded
placement and require Python 3; remote symlinks are rejected. An unavailable
resource displays an error without affecting the task session.

Workspace terminals mirror task-owned tmux panes without zooming or resizing
them. The GUI scrolls over the task's existing terminal dimensions; the TUI
shows a viewport of its shell. The ordinary TUI shell remains available via
**\\** for native terminal scrollback and other tmux interactions. A task needs
a running session before Shell can connect.
