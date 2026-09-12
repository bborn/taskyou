# lumen

AI summaries of a task's work, via [lumen](https://github.com/jnsahaj/lumen).

Three on-demand actions — press `A` in the detail view, search for them in the
palette (`>`), or run them from the CLI.

| Action | When to press it |
|---|---|
| **Explain changes** | "What did this task actually change?" — summarises staged changes, else the working tree, else the branch. |
| **Draft commit message** | You've staged something and want a message written for it. Prints it; does **not** commit. |
| **Review branch vs base** | Before opening a PR — summarises every commit on the branch, not just the uncommitted edits. |

## Prerequisites

```bash
brew install jnsahaj/lumen/lumen
```

Then a model. Either a local one (free, nothing leaves your machine):

```bash
brew install ollama && ollama serve && ollama pull qwen2.5-coder
```

…or a hosted provider key — see `config.example.env` for the full list.

## Install

```bash
cp -R examples/plugins/lumen ~/.config/task/plugins/
cp ~/.config/task/plugins/lumen/config.example.env \
   ~/.config/task/plugins/lumen/config.env
$EDITOR ~/.config/task/plugins/lumen/config.env   # pick a provider
ty plugins list                                    # should show 3 actions
```

`config.env` holds your key — keep it out of version control. (`ty plugins add`
git-clones a repo, so it can't install a subdirectory of this one; copy it.)

If you already ran `lumen configure`, its
`~/.config/lumen/lumen.config.json` is picked up and `config.env` is optional.

## Cost & privacy

**Every press calls a model.** With a hosted provider that means real money per
press and **your diff is sent to that provider**. If that isn't acceptable —
client work, private repos, anything under NDA — use the Ollama recipe, which
is local, free, and sends nothing anywhere.

## Limitations

- **This wraps lumen's AI text generation, not its diff viewer.** `lumen diff`
  and `lumen explain --list` are full-screen interactive TUIs that need a real
  terminal; ty captures an action's output rather than giving it a TTY, so
  neither can be exposed here. Run those directly in a shell.
- **Actions time out at 60s.** A comfortable margin for a hosted model on a
  normal diff (~5s measured), but a very large diff or a cold local model can
  exceed it. If that happens, stage a narrower change and press again.
- **On a pipeline step, "branch vs base" spans sibling steps.** ty's action
  environment carries no per-task base commit, so `review` diffs the whole
  branch — which on a shared pipeline branch already contains earlier steps'
  commits. Every action names the ref it summarised in its first line, so the
  scope is always visible rather than silently wrong.
- **Untracked files are invisible to git diff**, so a task whose entire output
  is new files reports "no changes" until you `git add` them. `explain` says so
  explicitly when it sees untracked files.

## lumen's observed contract

Measured against lumen 2.31.0 on 2026-07-20. `lib.sh` is built around this
table and points back at it.

| Command | Condition | Exit | Payload |
|---|---|---|---|
| `explain` / `explain --staged` / `explain <ref\|range>` | success | 0 | stdout |
| `draft [-c CTX]` | success | 0 | stdout (clean, no preamble) |
| `explain` | nothing to diff | 1 | stderr `diff is empty` |
| `explain --staged`, `draft` | nothing staged | 1 | stderr `diff (staged) is empty` |
| any | outside a git repo | 1 | stderr `not a repository` |
| `explain <bad ref>` | invalid reference | 1 | stderr `invalid reference: X` |
| any | no provider / bad key | 1 | stderr `AI request failed: …` |

Two consequences shape the scripts:

1. **`explain` writes a `# Entity:` / `# Provider:` header and a progress
   spinner to _stdout_**, ahead of the prose. ty shows an action's first line
   as the TUI banner, so `lumen_strip_preamble` removes it.
2. **ty merges stderr into stdout** (`CombinedOutput`), so every lumen call
   captures stderr separately and surfaces it only on failure — otherwise a
   provider warning would land in the banner.

Every failure is exit 1 with no further distinction, so the wrapper reports
lumen's own message verbatim instead of interpreting the code.

## Notes for plugin authors

This is the reference example for **wrapping a binary you can't assume is
installed**. The pattern in `lib.sh`:

- `lumen_preflight` checks worktree → bundled config → binary on PATH →
  provider configured, and `exit 0`s with one explanatory line at the first
  failure. A missing dependency is a message, never a stack trace.
- ty runs actions with cwd set to the *plugin* directory, so `cd
  "$WORKTREE_PATH"` is mandatory.
- Every invocation redirects stdin from `/dev/null`, so a subcommand that wants
  a TTY fails immediately instead of hanging until the timeout.
