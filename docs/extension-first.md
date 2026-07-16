# Extension-first: ty-chrome as a primary entry point

**Status:** design brief + first implementation slice (create-task / first-run
onboarding). See the companion PR touching `extensions/ty-chrome`.

## Goal

Make the [`ty-chrome`](../extensions/ty-chrome) extension a viable *primary*
way to use TaskYou: something a brand-new user can install from the Chrome Web
Store and get productive with, ideally without ever opening a terminal.

## The one hard constraint

The extension is a **thin client**. It has no backend of its own — everything
it does is an HTTP call to `ty serve` (the daemon) on localhost. The daemon is
what spawns Claude Code executors, runs tmux, manages git worktrees, and owns
the SQLite DB.

A Chrome extension is sandboxed. It **cannot**:

- start `ty serve` or `ty daemon`,
- run `git`, `tmux`, or spawn Claude Code,
- read or write arbitrary files.

So "install the extension and go, never touch a terminal" is **impossible via
the extension alone**. *Something* has to run the daemon. The onboarding blocker
is therefore **daemon bootstrap**, not the extension UI. Everything below is
about closing that gap.

Once a daemon is reachable, the extension is already close to a full client: it
lists tasks, matches browser tabs to tasks by dev-server port, embeds a live
xterm.js terminal into a task's tmux pane (Agent + Shell), annotates pages, and
bridges the executor to the user's real browser tabs. What it lacked — and what
this PR adds — is a way to **create** a task and a **first-run** experience when
nothing is running yet.

## Daemon-bootstrap options

### (a) Native app runs `ty serve` as a managed background service — **recommended near-term**

Ship the existing Tauri desktop app (`desktop/`) as the daemon host. It already
supervises both sidecars: `desktop/src-tauri/src/supervisor.rs` spawns
`ty serve --port <p>` and `ty daemon`, health-checks them, and adopts a
pre-existing daemon instead of double-starting. The onboarding story becomes
**"install two things"**:

1. Install the TaskYou desktop app (a normal signed `.dmg` / `.msi`, no
   terminal). It launches the daemon and keeps it alive.
2. Install the ty-chrome extension from the Web Store. It auto-discovers the
   daemon and you're working.

**Remaining work to make this true "no-terminal":**

- **Run the daemon as a real background service**, not just a GUI child.
  Today the supervisor terminates its children on app exit
  (`supervisor.rs:1-3`), so the daemon only lives while the window is open.
  Promote it to a login item / managed service so it survives:
  - macOS: a `launchd` LaunchAgent (`~/Library/LaunchAgents/…plist`, `RunAtLoad`
    + `KeepAlive`).
  - Linux: a `systemd --user` unit (`WantedBy=default.target`).
  - Windows: a Scheduled Task at logon, or a Service via the app installer.
  A `ty serve --install-service` / `--uninstall-service` subcommand that writes
  and loads the platform unit is the clean home for this; the desktop app calls
  it during first-run setup so the user never sees a shell.
- **Align the discovery port.** The desktop app defaults to `8484`
  (`supervisor.rs:11`), but the extension only probed `8080`/`8765`. This PR
  adds `8484` to the extension's `CANDIDATE_SERVERS` so the desktop-managed
  daemon is found out of the box.
- **Bundle a `ty` binary** with the desktop app (or install one) so the user
  doesn't need Homebrew/`go install` first.

Why recommended: lowest new surface area, reuses code that already exists,
keeps all execution local (the user's own machine, their own git checkouts,
their own Claude auth), and needs no new trust boundary. The cost is that it's
"two installs," not one.

### (b) Cloud daemon (extension → HTTPS)

Run `ty serve` on a hosted box; the extension points at
`https://<user>.taskyou.cloud` instead of localhost. This is the only option
that is *truly* zero-local-backend — install the extension, sign in, go.

Serious caveats, all of which are new surface area we don't have today:

- **Auth.** The extension would carry a bearer token / OAuth session to a
  remote agent. The daemon's HTTP API is currently unauthenticated because it
  binds to loopback; exposing it publicly requires real authn/authz on every
  route.
- **Driving the user's authenticated browser.** The killer feature is the
  browser bridge: the executor sees and drives the user's *logged-in* tabs
  (`sw.js` `browserExec`). With a cloud daemon, a remote agent is issuing
  commands that run against the user's authenticated sessions (their email,
  their bank, their admin panels). That is a large, sensitive trust boundary
  and needs an explicit, revocable consent model and a tight command allowlist.
- **Where does the code live / run?** Executors need a git checkout and a place
  to run Claude Code. Cloud means cloud checkouts, cloud secrets, cloud compute
  — a different product, not a config flag.

Good north star, wrong near-term bet: it turns a local dev tool into a hosted
multi-tenant service with a browser-driving remote agent. Revisit once the
local story is proven.

### (c) Native messaging host

Chrome's Native Messaging lets an extension talk to a locally-installed helper
binary over stdio. In principle the helper could start/stop `ty serve`.

Why it's a weak fit here:

- It **still requires a native install** (the messaging host manifest +
  binary), so it does not remove the "install something native" step that
  option (a) also has — but it delivers *less*, because…
- The extension already speaks HTTP to the daemon perfectly well. Native
  messaging would only be a bootstrap/launcher channel, duplicating what a
  service manager or the desktop app does more robustly.
- Native messaging hosts are **per-browser-profile** registered and famously
  fiddly (manifest paths, allowed-origins keyed to the extension ID, no clean
  cross-platform install). For a launcher, `launchd`/`systemd`/a login item is
  simpler and more reliable.

Net: it's a native install with none of the desktop app's benefits (no UI, no
supervision, no bundled binary). Skip it unless we specifically need the
extension to toggle the daemon on/off and refuse to ship any window.

## Recommendation

Ship **(a)**. Concretely:

1. This PR: create-task + first-run onboarding in the extension, and add `8484`
   to auto-discovery so the desktop-managed daemon is found.
2. Next: `ty serve --install-service` (launchd/systemd/Task Scheduler) invoked
   by the desktop app's first-run, so the daemon is a persistent background
   service. Bundle a `ty` binary with the app.
3. Later, as a separate product bet: evaluate (b) for a hosted offering, with a
   real auth model and an explicit consent flow for browser driving.

## Chrome Web Store readiness checklist

| Item | Status / action |
|---|---|
| **Single purpose** | State it plainly: *"Create and drive TaskYou coding tasks — annotate the task's dev-server pages and run its Claude Code terminal — from a side panel connected to a locally-running TaskYou daemon."* Everything in the extension serves that one purpose. |
| **Privacy policy** | Required (the store demands one for any extension with `host_permissions`). Key honest claim: the extension sends data **only** to the user-configured `ty serve` origin (localhost by default). No analytics, no third-party servers, no remote telemetry. Page content / screenshots / console logs are captured **on demand** and posted to the local daemon only. Publish it and link it in the listing. |
| **`<all_urls>` host permission** | Currently requested so annotate + the browser bridge work on any dev-server host (`localhost:<port>`, `*.test`, plus whatever external site the executor is asked to look at). This is the hardest review item — broad host access on a screenshot-capable extension gets scrutiny. **Recommended narrowing:** drop `<all_urls>` from the manifest and switch to **`activeTab` + `optional_host_permissions: ["<all_urls>"]`**, requesting broad access only when the user actually starts the bridge / annotate on a page. `activeTab` covers "act on the tab the user just invoked me on" without a scary install-time prompt. |
| **`scripting`** | Justified: injecting the annotate overlay and the command bridge into the matched tab is core function. Keep, and say so in the justification. Pairs with `activeTab` cleanly. |
| **`tabs` / `tabGroups`** | Justified by the visible, user-revocable "ty #<id>" tab group that bounds what the executor can drive. Explain the boundary in the listing — it's a *privacy feature*, not a grab. |
| **Remote code** | None. All JS is vendored in the package (`vendor/`), no CDN, no `eval`. Say so — it speeds review. |
| **Data handling disclosures** | In the Web Store form, declare: handles "website content" (screenshots/DOM), sent only to the user's own local server; no data sold; no data used for unrelated purposes. |
| **Precedent** | [nanobrowser](https://chromewebstore.google.com/detail/nanobrowser/…) is already in the Web Store and drives the user's *real authenticated browser* to complete tasks — a strictly broader capability than ours. It's concrete evidence that a browser-driving, `<all_urls>`-class extension is publishable when the purpose is clear and disclosed. Cite it if review pushes back on the bridge. |
| **Screenshots / listing** | Show the side panel: matched task, embedded terminal, annotate flow, and the tab-group boundary. Make the "connects to a local daemon you run" model obvious so reviewers understand the architecture. |

**Bottom line for the store:** narrow to `activeTab` + optional `<all_urls>`,
ship a privacy policy that truthfully says "talks only to your local daemon,"
and lead the single-purpose description with task creation + execution. The
capability is publishable — nanobrowser is the proof.
