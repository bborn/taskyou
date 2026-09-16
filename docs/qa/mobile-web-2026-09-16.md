# Mobile web dogfood — September 16, 2026

Tested the current React frontend against a separate SQLite database and local API, with 65 synthetic tasks across three projects. No real tasks or agent sessions were changed. Browser: Chromium, iPhone user agent plus explicit CDP touch emulation. Viewports: 320×568, 375×420, 390×844, 375×320, 844×390, and 1440×900.

## Fixed

- **Multi-word search lost spaces while typing.** The filter string was trimmed on every keystroke. The input now preserves raw edits while the board receives its normalized filter.
- **Task-number searches showed zero filter counts.** Counts now use the same filter parser as the board, including task IDs, project matching, and status aliases. “All projects” counts across projects, rather than repeating the current project's count.
- **New tasks had no mobile attachment picker.** Added an Add files control, named file removal controls, and wrapping for long filenames.
- **Existing attachments were inaccessible on phones.** Added Attachments to the task actions sheet, including viewing, uploading, and deleting. Delete failures now produce an error instead of silently doing nothing.
- **Failed uploads falsely reported success.** Pending files remain in the form. Retry reuses the saved task ID and skips files already uploaded, avoiding duplicate tasks.
- **Unsent replies vanished on navigation or reload.** Drafts are retained per task in session storage, removed when cleared or successfully sent. A quick “yes” or “continue” preserves a different draft being composed.
- **Global search clipped results in short viewports.** The results list now shrinks inside its sheet and remains scrollable. Its input is 16px on phones, avoiding the small-input condition that triggers Safari focus zoom. The accessible title and description now live inside the dialog.
- **Tiny sheet controls.** Close and file-removal targets are 44px on phones; Advanced has a 44px minimum height. Form title/description labels are connected to their inputs.

## Verification

`check-mobile-web.py` reproduces real keystrokes, verifies counts, attachment recovery, narrow layouts, search geometry, touch Return, and draft persistence. Upload and reply failure/success responses are simulated in the browser so these regression checks do not mutate backend task data. A real PNG upload, forced network failure, retry, and viewing the uploaded attachment were also exercised against the disposable API.

```sh
python3 scripts/qa/check-mobile-web.py http://127.0.0.1:1431 \
  --task-id 1 --query 'mobile qa' --live-task-id 4
```

Use an existing non-archived fixture whose title contains the multi-word query, plus an optional processing/blocked fixture. Requires agent-browser, Python 3, and Node 22+. The UI and API should be served at the same origin; `--api-base` supports a separate development API. The test browser closes automatically.

- Browser regression runner: all 8 groups pass against the production bundle.
- Production TypeScript/Vite build: passes (existing CSS, chunk-size, and mixed-import warnings).
- Existing frontend unit tests: 4 pass.
- Full `go test -race ./...`: fails in unchanged TUI test `TestRemoteAttachDropsALeftOverViewPairing` (`remote pane in window , TUI in @0`). The same test fails when run alone. All other package results pass, including parity and HTTP API tests.
- `golangci-lint run`: blocked by the installed linter being built with Go 1.25 while this repository targets Go 1.26.

## Remaining device verification and follow-up

This pass does **not** establish native Safari or installed-PWA readiness. Xcode's iOS simulator is unavailable on this machine. A short viewport tests constrained geometry, not the native keyboard, visual viewport panning, address-bar collapse, or home-indicator insets. Those need an actual iPhone or simulator.

Additional observed behaviors worth a focused follow-up:

- Browser Back can change the underlying page while a task edit sheet remains open; task-form drafts are not persisted when explicitly dismissed. Reply drafts are protected by this change.
- At 844px landscape width, the existing breakpoint selects the horizontally scrolling desktop board. No orientation policy was changed in this pass.
- Agent execution, real model replies, and OS photo-picker permissions were not exercised. Reply transport outcomes were mocked; file bytes were uploaded through the real API.

Changes are local; no live server has been deployed by this QA run.
