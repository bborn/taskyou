# GitHub traction snapshots

This directory contains a bounded, read-only measurement command for TaskYou. It uses the authentication already held by `gh` and queries repository metadata, GitHub's traffic endpoints, and release asset counters. Each request has a 30-second timeout. The command does not add telemetry, schedule itself, or change GitHub or the TaskYou product.

Run it from the repository root:

```bash
python3 scripts/metrics/github_traction.py
```

The command prints a short report and writes a raw JSON observation plus the same Markdown report to `.taskyou-metrics/`. That directory is gitignored because observations are for local review. The files contain response data and errors, but never authentication headers, environment variables, or tokens. Use `--repo owner/name` or `--output-dir path` when needed.

Each snapshot records `captured_at`; each source records availability or an error and its source window. [GitHub documents repository traffic as a rolling 14-day view](https://docs.github.com/en/rest/metrics/traffic). The views and clones payloads supply the actual returned dates; referrer and path endpoints omit dates, so the command labels dates inferred from matching views and clones series. Repository and release counters are point-in-time observations.

Read each snapshot independently. In particular:

- The API's aggregate `uniques` value describes the whole returned window. Daily unique values cannot be added to reproduce it because the same visitor can appear on several days.
- Consecutive snapshots usually have overlapping 14-day windows, so adding snapshots double-counts activity.
- Clones and release asset downloads do not establish installs, users, successful setup, or activation.
- Maintainer and CI actions contaminate these small counts. Add known smoke tests to your interpretation notes; GitHub does not identify which download or clone came from them.

The current GitHub data cannot answer how many people visit taskyou.dev or complete their first TaskYou task. Measuring those would require a separately approved site counter and product event, with privacy behavior and opt-out defined before implementation.

Run the focused fixture tests with:

```bash
python3 -m unittest scripts/metrics/test_github_traction.py
```
