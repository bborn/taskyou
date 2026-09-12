# shellcheck shell=bash
#
# Shared helpers for the lumen plugin. Sourced by the action scripts; this file
# is never executed directly and must have no top-level side effects.
#
# The lumen behaviour these helpers are built around — exit codes, which stream
# each payload lands on, the `explain` preamble — is tabulated in README.md
# under "lumen's observed contract"; the helpers below cite it where it bites.
#
# Provider credentials come from LUMEN_API_KEY / LUMEN_AI_PROVIDER /
# LUMEN_AI_MODEL, from ./lumen.config.json or ~/.config/lumen/lumen.config.json,
# or from the provider's own key variable (e.g. GEMINI_API_KEY).

# lumen_preflight: bail out cleanly (exit 0, one explanatory line) unless we
# have a worktree, the lumen binary, and some sign of a configured provider.
# On success the cwd is the task's worktree — ty runs actions with cwd set to
# the *plugin* directory, so this cd is mandatory, not cosmetic.
lumen_preflight() {
  local wt="${WORKTREE_PATH:-}"
  if [[ -z "$wt" || ! -d "$wt" ]]; then
    echo "no worktree for this task"
    exit 0
  fi
  cd "$wt"

  # Load bundled config, if present (same idiom as the slack example).
  if [[ -n "${TASK_PLUGIN_DIR:-}" && -f "$TASK_PLUGIN_DIR/config.env" ]]; then
    # shellcheck disable=SC1091
    source "$TASK_PLUGIN_DIR/config.env"
  fi

  if ! command -v lumen >/dev/null 2>&1; then
    echo "lumen not installed — brew install jnsahaj/lumen/lumen"
    exit 0
  fi

  if ! lumen_have_provider; then
    echo "no lumen provider configured — see config.example.env"
    exit 0
  fi
}

# lumen_have_provider: true when *any* credential source lumen understands is
# present. Deliberately generous — a false positive just means lumen's own
# (good) error message surfaces, while a false negative would refuse to run on
# a working setup.
lumen_have_provider() {
  # Ollama runs locally and needs no key.
  [[ -n "${LUMEN_API_KEY:-}" || "${LUMEN_AI_PROVIDER:-}" == "ollama" ]] && return 0
  [[ -f ./lumen.config.json ]] && return 0
  [[ -f "${XDG_CONFIG_HOME:-$HOME/.config}/lumen/lumen.config.json" ]] && return 0

  local var
  for var in OPENAI_API_KEY GEMINI_API_KEY ANTHROPIC_API_KEY GROQ_API_KEY \
    DEEPSEEK_API_KEY OPENROUTER_API_KEY XAI_API_KEY; do
    [[ -n "${!var:-}" ]] && return 0
  done
  return 1
}

# lumen_count: count lines on stdin, without wc's leading padding.
lumen_count() { wc -l | tr -d ' '; }

# lumen_base: echo the merge-base of this branch and its base branch, or return
# 1. origin/HEAD is routinely unresolvable in a ty worktree (branch never
# pushed, detached HEAD, no remote), so every caller must handle the failure.
lumen_base() { git merge-base origin/HEAD HEAD 2>/dev/null; }

# lumen_commits_since: how many commits HEAD is ahead of $1 (0 if unknowable).
lumen_commits_since() { git rev-list --count "$1..HEAD" 2>/dev/null || echo 0; }

# lumen_diff_target: pick what to summarise, first non-empty wins.
#
#   1. staged changes    2. working tree    3. this branch vs its base
#
# A mode is nothing more than the one argument `git diff` and `lumen explain`
# each need for it — which differ (--cached vs --staged) — so each mode is
# declared once, here, and the two consumers below just splat it. Returns 1
# when there is nothing to diff at all; the caller prints one line and exits 0.
#
# Sharp edge, deliberately surfaced rather than hidden: on a pipeline step the
# shared branch already carries sibling steps' commits, so mode 3's range spans
# more than this task's work. That is why every action names the ref it
# summarised in its banner — the output is never ambiguous about its scope.
# shellcheck disable=SC2034  # LUMEN_* globals are read by the action scripts.
lumen_diff_target() {
  LUMEN_DIFF_ARG=""
  LUMEN_EXPLAIN_ARG=""
  LUMEN_TARGET_LABEL=""

  if ! git diff --cached --quiet 2>/dev/null; then
    LUMEN_DIFF_ARG="--cached"
    LUMEN_EXPLAIN_ARG="--staged"
    LUMEN_TARGET_LABEL="staged changes"
    return 0
  fi

  if ! git diff --quiet 2>/dev/null; then
    LUMEN_TARGET_LABEL="working tree"
    return 0
  fi

  local base
  if base=$(lumen_base) && [[ "$(lumen_commits_since "$base")" != "0" ]]; then
    LUMEN_DIFF_ARG="$base..HEAD"
    LUMEN_EXPLAIN_ARG="$base..HEAD"
    LUMEN_TARGET_LABEL="origin/HEAD..HEAD"
    return 0
  fi

  return 1
}

# lumen_changed_files: how many files the selected target touches.
#
# ${x:+"$x"} passes the argument when there is one and nothing at all when
# there isn't: working-tree mode takes no argument, and a quoted "" would be an
# empty pathspec rather than no pathspec. Same splat in lumen_explain_target.
lumen_changed_files() {
  git diff --name-only ${LUMEN_DIFF_ARG:+"$LUMEN_DIFF_ARG"} | lumen_count
}

# lumen_explain_target: run `lumen explain` against the selected target.
lumen_explain_target() {
  lumen_run explain ${LUMEN_EXPLAIN_ARG:+"$LUMEN_EXPLAIN_ARG"}
}

# lumen_run: the single place lumen is invoked.
#
# stdout is captured, stderr goes to a temp file, and stdin is /dev/null so a
# subcommand that wants a TTY fails fast instead of hanging until the 60s
# action timeout. The stderr capture is not optional: ty's RunAction uses
# CombinedOutput(), so an unredirected provider warning would land in the TUI
# banner. Every lumen failure is exit 1 with no further distinction, so on
# failure we surface lumen's own first stderr line rather than read the code.
lumen_run() {
  local errf rc out msg
  errf=$(mktemp -t lumen-err) || {
    echo "cannot create temp file"
    exit 0
  }

  # These scripts run under `set -e`; fence the call so a non-zero exit reaches
  # the branch below instead of aborting before anything is reported.
  set +e
  out=$(lumen "$@" 2>"$errf" </dev/null)
  rc=$?
  set -e

  if [[ $rc -ne 0 ]]; then
    # lumen writes `<colour><CR>error:<reset> <message>` — strip the colour
    # codes and the stray CR before the `error:` prefix can be matched.
    msg=$(sed -e $'s/\033\\[[0-9;]*m//g' -e $'s/\r//g' -e 's/^error: *//' "$errf" |
      grep -v '^[[:space:]]*$' | head -n1)
    rm -f "$errf"
    echo "lumen failed: ${msg:-exit $rc}"
    exit "$rc"
  fi

  rm -f "$errf"
  printf '%s\n' "$out" | lumen_strip_preamble
}

# lumen_strip_preamble: drop lumen's header and progress spinner.
#
# The spinner is a single physical line full of carriage returns, emitted after
# the header and before the prose, so "everything through the last CR-bearing
# line" removes both in one rule and is a no-op for `draft`, whose output has
# no spinner. Leading blanks and any stray header lines are then trimmed so
# line 1 of what the user sees is real content.
lumen_strip_preamble() {
  awk '
    # start must be initialised: an uninitialised awk variable used as an index
    # yields line[""], not line[0], which silently swallows spinner-free output
    # such as every `draft` result.
    BEGIN { n = 0; start = 0 }
    { line[n++] = $0; if (index($0, "\r") > 0) start = n }
    END {
      for (i = start; i < n; i++) {
        if (!seen && (line[i] == "" || line[i] ~ /^# (Entity|Provider):/ || line[i] ~ /^-+$/)) continue
        seen = 1
        print line[i]
      }
    }
  '
}

# lumen_banner: print one line, truncated to 80 columns — ty shows an action's
# first line as the TUI banner, where anything longer is cut off anyway.
lumen_banner() {
  printf '%.80s\n' "$1"
}
