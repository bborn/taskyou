# iPhone Safari on GitHub Actions

Runs TaskYou's built web UI and isolated SQLite API in a real iOS Simulator.
Appium uses native taps and forces the software keyboard on. The checks require
both a visible native keyboard and visual-viewport shrink, then verify the
focused filter/title/description stays on screen without Safari zoom. Screenshots
include the device UI. This is simulator Safari coverage, not physical-device or
installed-PWA certification.

## Cost and execution controls

- Only runs when the repository's visibility is **public**, where standard
  GitHub-hosted macOS compute is free. A private repository skips the job.
- Standard `macos-15` only; no larger/paid runner, matrix, cron, or PR trigger.
- Manual dispatch after this workflow is on the default branch. Before then,
  push to `codex/ios-safari-qa` with `[ios-qa]` in the HEAD commit message.
  Ordinary pushes to that branch create a skipped run.
- 25-minute job limit, bounded steps, one concurrent run (new run cancels old).
- No automatic retries and no Actions caches. Only preinstalled simulator
  runtimes matching the selected Xcode SDK are used; absence fails instead of
  downloading a runtime. Appium uses its official prebuilt arm64 helper (pinned
  version and SHA-256), avoiding a fresh Xcode helper build on every run.
- Evidence is capped at 15 MB/run, logs at 1 MB/file, retained for one day.
  Artifact storage is separate from compute billing; delete downloaded artifacts
  after reviewing them to avoid accumulating even this small allowance.
- No production secrets, live DB, or executor daemon. Synthetic tasks only.

```sh
gh workflow run ios-safari.yml --ref <branch>
gh run list --workflow ios-safari.yml --limit 3
gh run download <run-id> --name iphone-safari-<run-id> --dir /tmp/ios-evidence
```

Inspect `result.json` and the PNGs together. Failure screenshots and viewport
measurements distinguish application layout bugs from automation setup problems.
Appium logs are diagnostic, not proof of a passing UI test.
