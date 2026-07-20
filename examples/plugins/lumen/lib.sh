# shellcheck shell=bash
#
# Shared helpers for the lumen plugin. Sourced by the action scripts; this file
# is never executed directly and must have no top-level side effects.
#
# ---------------------------------------------------------------------------
# lumen's observed contract (verified against lumen 2.31.0, 2026-07-20)
# ---------------------------------------------------------------------------
#
#   command                     | condition           | exit | payload stream
#   ----------------------------+---------------------+------+----------------
#   lumen explain               | success             |  0   | stdout
#   lumen explain --staged      | success             |  0   | stdout
#   lumen explain <ref|range>   | success             |  0   | stdout
#   lumen draft [-c CTX]        | success             |  0   | stdout (clean)
#   lumen explain               | nothing to diff     |  1   | stderr "diff is empty"
#   lumen explain --staged      | nothing staged      |  1   | stderr "diff (staged) is empty"
#   lumen draft                 | nothing staged      |  1   | stderr "diff (staged) is empty"
#   any                         | outside a git repo  |  1   | stderr "not a repository"
#   lumen explain <bad ref>     | invalid reference   |  1   | stderr "invalid reference: X"
#   any                         | no provider/API key |  1   | stderr "AI request failed: ..."
#
# Every failure is exit 1 with an ANSI-coloured `error: <msg>` on stderr and an
# empty stdout. Because the codes carry no information, lumen_run surfaces
# lumen's own message verbatim rather than trying to interpret the code.
#
# Two properties of that table drive the helpers below:
#
#  1. `explain` writes a `# Entity:` / `# Provider:` header and an in-band
#     progress spinner to *stdout*, ahead of the prose. ty shows line 1 of an
#     action's output as the TUI banner, so that preamble has to go —
#     see lumen_strip_preamble. (`draft` output is already clean.)
#  2. ty's RunAction uses exec.Cmd.CombinedOutput(), which merges stderr into
#     what the user sees. Every lumen call therefore captures stderr into a
#     temp file so it only ever surfaces on failure — see lumen_run.
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
  if [[ -n "${LUMEN_API_KEY:-}" ]]; then
    return 0
  fi
  # Ollama runs locally and needs no key.
  if [[ "${LUMEN_AI_PROVIDER:-}" == "ollama" ]]; then
    return 0
  fi
  if [[ -f ./lumen.config.json || -f "${XDG_CONFIG_HOME:-$HOME/.config}/lumen/lumen.config.json" ]]; then
    return 0
  fi
  local var
  for var in OPENAI_API_KEY GEMINI_API_KEY ANTHROPIC_API_KEY GROQ_API_KEY \
    DEEPSEEK_API_KEY OPENROUTER_API_KEY XAI_API_KEY; do
    if [[ -n "${!var:-}" ]]; then
      return 0
    fi
  done
  return 1
}

# lumen_diff_target: pick what to summarise, first non-empty wins.
#
#   1. staged changes    2. working tree    3. this branch vs its base
#
# Sets LUMEN_MODE (staged|worktree|range), LUMEN_RANGE (range mode only) and
# LUMEN_TARGET_LABEL, and returns 0. Returns 1 when there is nothing to diff at
# all; the caller is expected to print one line and exit 0.
#
# Sharp edge, deliberately surfaced rather than hidden: on a pipeline step the
# shared branch already carries sibling steps' commits, so mode 3's range spans
# more than this task's work. That is why every action names the ref it
# summarised in its banner — the output is never ambiguous about its scope.
# shellcheck disable=SC2034  # LUMEN_* globals are read by the action scripts.
lumen_diff_target() {
  LUMEN_MODE=""
  LUMEN_RANGE=""
  LUMEN_TARGET_LABEL=""

  if ! git diff --cached --quiet 2>/dev/null; then
    LUMEN_MODE="staged"
    LUMEN_TARGET_LABEL="staged changes"
    return 0
  fi

  if ! git diff --quiet 2>/dev/null; then
    LUMEN_MODE="worktree"
    LUMEN_TARGET_LABEL="working tree"
    return 0
  fi

  # origin/HEAD is routinely unresolvable in a ty worktree (branch never
  # pushed, detached HEAD, no remote) — fall through rather than erroring.
  local base
  if base=$(git merge-base origin/HEAD HEAD 2>/dev/null); then
    if [[ "$(git rev-list --count "$base..HEAD" 2>/dev/null || echo 0)" != "0" ]]; then
      LUMEN_MODE="range"
      LUMEN_RANGE="$base..HEAD"
      LUMEN_TARGET_LABEL="origin/HEAD..HEAD"
      return 0
    fi
  fi

  return 1
}

# lumen_changed_files: how many files the selected target touches.
lumen_changed_files() {
  case "$LUMEN_MODE" in
  staged) git diff --cached --name-only ;;
  worktree) git diff --name-only ;;
  range) git diff --name-only "$LUMEN_RANGE" ;;
  esac | wc -l | tr -d ' '
}

# lumen_explain_target: run `lumen explain` against the selected target. The
# three modes need three different argument shapes, so this dispatches rather
# than splatting a string.
lumen_explain_target() {
  case "$LUMEN_MODE" in
  staged) lumen_run explain --staged ;;
  worktree) lumen_run explain ;;
  range) lumen_run explain "$LUMEN_RANGE" ;;
  esac
}

# lumen_run: the single place lumen is invoked.
#
# stdout is captured, stderr goes to a temp file, and stdin is /dev/null so a
# subcommand that wants a TTY fails fast instead of hanging until the 60s
# action timeout. On success the preamble is stripped; on failure lumen's own
# first stderr line is surfaced and the temp file is removed either way.
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
    msg=$(sed -e $'s/\033\\[[0-9;]*m//g' -e 's/^error: *//' "$errf" |
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
