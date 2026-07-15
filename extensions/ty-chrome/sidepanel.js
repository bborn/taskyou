// ty-chrome side panel: shows the task matched to the active tab, sends
// annotations, and embeds a full interactive terminal into the task — a real
// xterm.js terminal wired to the task's Claude (Agent) or workdir Shell tmux
// pane over the daemon's capture-pane WebSocket. Run Claude Code, shell
// commands, anything, without leaving Chrome.

const $ = (id) => document.getElementById(id);

let activeTabId = null;
let currentTask = null;
let reconnectTimer = null;

const send = (msg) => chrome.runtime.sendMessage(msg);

// --- State refresh -----------------------------------------------------------

// ?tab=<id> pins the panel to a specific tab — used when opening
// sidepanel.html as a regular tab (debugging / scripted demos).
const forcedTabId = new URLSearchParams(location.search).get('tab');

async function getActiveTab() {
  if (forcedTabId) {
    try {
      return await chrome.tabs.get(Number(forcedTabId));
    } catch {
      return null;
    }
  }
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab || null;
}

function updateCount(count) {
  const chip = $('annotation-count');
  chip.textContent = count;
  chip.classList.toggle('zero', count === 0);
  $('send-btn').disabled = count === 0 || !currentTask;
}

async function refresh() {
  const tab = await getActiveTab();
  activeTabId = tab?.id ?? null;
  const state = await send({ type: 'getState', tabId: activeTabId });

  $('status-dot').classList.toggle('ok', !!state.connected);
  $('status-dot-fallback').classList.toggle('ok', !!state.connected);
  $('server-url').value = state.serverUrl;
  try {
    $('server-label').textContent = new URL(state.serverUrl).host;
  } catch {
    $('server-label').textContent = state.serverUrl;
  }
  $('no-connection').classList.toggle('hidden', !!state.connected);

  // While the bridge is running it may foreground another tab (to screenshot a
  // docs tab, say) whose URL doesn't port-match — keep showing the pinned task
  // so the executor console doesn't blank out mid-session.
  currentTask = state.task || (bridgeActive ? bridgeTask : null);
  renderTask();
  updateCount(state.annotationCount);

  if (!currentTask && state.connected) loadCandidates();
  syncTerminal(state.connected);
  if (state.connected && currentTask) bridgeLoop();

  // Self-heal: keep retrying while disconnected (auto-discovery runs in the SW)
  clearTimeout(reconnectTimer);
  if (!state.connected && document.visibilityState === 'visible') {
    reconnectTimer = setTimeout(refresh, 3000);
  }
}

function renderTask() {
  const match = $('task-match');
  const picker = $('task-picker');
  if (currentTask) {
    match.classList.remove('hidden');
    picker.classList.add('hidden');
    $('task-id').textContent = `#${currentTask.id}`;
    $('task-title').textContent = currentTask.title;
    $('task-status').textContent = currentTask.status;
    $('task-status').classList.toggle('blocked', currentTask.status === 'blocked');
    $('task-port').textContent = currentTask.port ? `:${currentTask.port}` : '';
    $('task-branch').textContent = currentTask.branch_name || '';
  } else {
    match.classList.add('hidden');
    picker.classList.remove('hidden');
  }
}

async function loadCandidates() {
  const { tasks } = await send({ type: 'listCandidateTasks' });
  const sel = $('task-select');
  sel.innerHTML = '<option value="">Pick a task…</option>';
  for (const t of tasks || []) {
    const o = document.createElement('option');
    o.value = t.id;
    o.textContent = `#${t.id} ${t.title} (${t.status}${t.port ? `, :${t.port}` : ''})`;
    sel.appendChild(o);
  }
}

// --- Embedded terminal ---------------------------------------------------------
//
// A real xterm.js terminal wired to the task's tmux pane over the daemon's
// capture-pane WebSocket (GET /api/tasks/{id}/terminal[?pane=shell]). This is
// the browser-fallback transport the desktop/web GUI also uses: keystrokes flow
// out via tmux send-keys, the visible pane streams back on a 500ms tick. The
// same rendering the TUI shows — Claude Code included — but inside Chrome.

const XTERM_THEME = {
  background: '#0b1220',
  foreground: '#cbd5e1',
  cursor: '#cbd5e1',
  selectionBackground: '#33467c',
  black: '#15161e', red: '#f7768e', green: '#9ece6a', yellow: '#e0af68',
  blue: '#7aa2f7', magenta: '#bb9af7', cyan: '#7dcfff', white: '#a9b1d6',
  brightBlack: '#414868', brightRed: '#f7768e', brightGreen: '#9ece6a',
  brightYellow: '#e0af68', brightBlue: '#7aa2f7', brightMagenta: '#bb9af7',
  brightCyan: '#7dcfff', brightWhite: '#c0caf5',
};

// The vendored addon UMD builds expose their class under a namespace object
// (`window.FitAddon.FitAddon`, webpack library target), whereas xterm spreads
// `Terminal` onto the global directly. Normalize both to a constructor.
const FitAddonCtor = typeof FitAddon === 'function' ? FitAddon : FitAddon.FitAddon;
const Unicode11AddonCtor =
  typeof Unicode11Addon === 'function' ? Unicode11Addon : Unicode11Addon.Unicode11Addon;

let term = null; // xterm.js Terminal
let fit = null; // FitAddon
let ws = null; // active terminal WebSocket
let termPane = 'agent'; // 'agent' | 'shell'
let termBoundTaskId = null; // task the terminal is currently attached/attaching to
let termWaitTimer = null; // retry timer while waiting for a session/pane
let termResizeObs = null;

function setTermState(text, cls) {
  const el = $('executor-state');
  el.textContent = text || '';
  el.classList.remove('live', 'busy');
  if (cls) el.classList.add(cls);
}

// Show a centered message (and optional action button) over the terminal.
// Pass msg === null to hide the overlay and reveal the live terminal.
function showTermOverlay(msg, actionLabel, actionFn) {
  const overlay = $('term-overlay');
  const btn = $('term-action');
  if (msg == null) {
    overlay.classList.add('hidden');
    return;
  }
  overlay.classList.remove('hidden');
  $('term-msg').textContent = msg;
  if (actionLabel) {
    btn.textContent = actionLabel;
    btn.classList.remove('hidden');
    btn.onclick = actionFn;
  } else {
    btn.classList.add('hidden');
    btn.onclick = null;
  }
}

function teardownSocket() {
  if (ws) {
    ws.onclose = null;
    ws.onmessage = null;
    ws.onerror = null;
    try { ws.close(); } catch {}
    ws = null;
  }
}

function detachTerminal() {
  teardownSocket();
  clearTimeout(termWaitTimer);
  termWaitTimer = null;
  if (termResizeObs) {
    termResizeObs.disconnect();
    termResizeObs = null;
  }
  if (term) {
    term.dispose();
    term = null;
    fit = null;
  }
}

function buildTerm() {
  const host = $('term-host');
  if (!host) return null;
  if (term) {
    term.dispose();
    term = null;
  }
  term = new Terminal({
    theme: XTERM_THEME,
    allowProposedApi: true,
    fontFamily: 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Monaco, "Cascadia Code", monospace',
    fontSize: 11.5,
    cursorBlink: true,
    macOptionIsMeta: true,
    scrollback: 2000,
  });
  try {
    // Unicode 11 width tables so Claude Code's glyphs (⏺, ✶, spinners) don't
    // overlap or leave gaps under xterm's legacy width handling.
    term.loadAddon(new Unicode11AddonCtor());
    term.unicode.activeVersion = '11';
  } catch {}
  fit = new FitAddonCtor();
  term.loadAddon(fit);
  term.open(host);
  try { fit.fit(); } catch {}
  if (!termResizeObs) {
    termResizeObs = new ResizeObserver(() => {
      try { fit && fit.fit(); } catch {}
    });
    // Observe the sized wrapper (the host is inset:0 within it).
    termResizeObs.observe(host.parentElement || host);
  }
  return term;
}

// Attach the terminal to termBoundTaskId's current pane (agent/shell),
// resolving session state first and offering to start one when missing.
async function attachTerminal() {
  const taskId = termBoundTaskId;
  if (taskId == null) return;
  teardownSocket();
  clearTimeout(termWaitTimer);
  termWaitTimer = null;
  setTermState('', null);
  showTermOverlay('Connecting…');

  const infoRes = await send({ type: 'terminalInfo', taskId });
  if (taskId !== termBoundTaskId) return; // task switched mid-await
  if (infoRes.error) {
    showTermOverlay(infoRes.error, 'Retry', attachTerminal);
    return;
  }
  const info = infoRes.info;

  if (!info.window_exists) {
    // Pane borrowed by an open TUI detail view — it returns to a daemon window
    // when released; poll until then.
    if (info.pane_borrowed_by) {
      showTermOverlay(`Executor is attached to the TUI (${info.pane_borrowed_by}). It appears here once released.`);
      setTermState('● running elsewhere', 'busy');
      termWaitTimer = setTimeout(() => { if (taskId === termBoundTaskId) attachTerminal(); }, 2500);
      return;
    }
    // A queued/processing task is still spinning up — wait for its executor.
    const status = currentTask?.status;
    if (status === 'queued' || status === 'processing') {
      showTermOverlay('Waiting for the executor to start…');
      termWaitTimer = setTimeout(() => { if (taskId === termBoundTaskId) attachTerminal(); }, 2500);
      return;
    }
    // Idle task with no session: offer to start one.
    showTermOverlay(
      termPane === 'shell'
        ? 'No session running — the shell opens alongside the executor.'
        : 'No executor session running for this task.',
      'Start session',
      startTermSession,
    );
    return;
  }

  // Window is live. The Shell tab needs its workdir pane materialized first.
  let resolved = info;
  if (termPane === 'shell') {
    const shellRes = await send({ type: 'ensureShellPane', taskId });
    if (taskId !== termBoundTaskId) return;
    if (shellRes.error) {
      showTermOverlay(shellRes.error, 'Retry', attachTerminal);
      return;
    }
    resolved = shellRes.info;
  }
  const paneId = termPane === 'shell' ? resolved.shell_pane_id : resolved.claude_pane_id;
  if (!paneId) {
    showTermOverlay(
      termPane === 'shell' ? 'No shell pane available.' : 'No executor pane available.',
      'Retry',
      attachTerminal,
    );
    return;
  }
  openTerminalSocket(taskId);
}

async function startTermSession() {
  const taskId = termBoundTaskId;
  showTermOverlay('Starting session…');
  const r = await send({ type: 'ensureSession', taskId });
  if (taskId !== termBoundTaskId) return;
  if (r.error) {
    showTermOverlay(r.error, 'Retry', startTermSession);
    return;
  }
  attachTerminal();
}

async function openTerminalSocket(taskId) {
  const { serverUrl, connected } = await send({ type: 'getServerUrl' });
  if (taskId !== termBoundTaskId) return;
  if (!connected || !serverUrl) {
    showTermOverlay('Waiting for ty serve…', 'Retry', attachTerminal);
    return;
  }
  let t;
  try {
    t = buildTerm();
  } catch (e) {
    showTermOverlay('Terminal failed to initialize: ' + e.message, 'Retry', attachTerminal);
    return;
  }
  if (!t) return;

  const wsBase = serverUrl.replace(/^http/, 'ws');
  const paneQuery = termPane === 'shell' ? '?pane=shell' : '';
  const socket = new WebSocket(`${wsBase}/api/tasks/${taskId}/terminal${paneQuery}`);
  ws = socket;

  socket.onmessage = (event) => {
    const data = String(event.data);
    if (data[0] === '{') {
      try {
        const m = JSON.parse(data);
        if (m.type === 'size') return; // we drive sizing via resize messages
      } catch {
        // fall through: screen content that happens to start with "{"
      }
    }
    t.write(data);
    trackExecutorActivity(data);
  };
  socket.onclose = () => {
    if (ws === socket) {
      ws = null;
      setTermState('', null);
      showTermOverlay('Terminal disconnected', 'Reconnect', attachTerminal);
    }
  };
  socket.onerror = () => { /* onclose fires next; message shown there */ };
  socket.onopen = () => {
    showTermOverlay(null);
    setTermState('● live', 'live');
    try { socket.send(JSON.stringify({ type: 'resize', cols: t.cols, rows: t.rows })); } catch {}
    t.focus();
  };
  t.onData((d) => {
    if (socket.readyState === WebSocket.OPEN) socket.send(d);
  });
  t.onResize(({ cols, rows }) => {
    if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', cols, rows }));
  });
}

// Bind the terminal to the panel's current task. Only (re)attaches when the
// bound task actually changes, so incidentally switching Chrome tabs (which can
// momentarily resolve no task) never tears down a live session mid-command.
function syncTerminal(connected) {
  if (!connected) {
    if (termBoundTaskId == null) showTermOverlay('Waiting for ty serve…');
    return;
  }
  const taskId = currentTask?.id ?? null;
  if (taskId == null) {
    if (termBoundTaskId == null) showTermOverlay('Match a tab or pick a task to open its terminal.');
    return;
  }
  if (taskId !== termBoundTaskId) {
    termBoundTaskId = taskId;
    attachTerminal();
  }
}

// Auto-reload: when the Agent pane transitions working→idle, its batch of edits
// is done — reload the app tab so the user sees the result. Never reload while
// annotations are still pinned (unsent work on the page). Driven off the same
// TUI footer markers the mirror streams back.
let executorBusy = false;
let idleStreak = 0;

function maybeAutoReload() {
  if (!$('auto-reload').checked || activeTabId == null) return;
  if (!$('annotation-count').classList.contains('zero')) return;
  chrome.tabs.reload(activeTabId);
  setTermState('↻ page reloaded', 'live');
}

function trackExecutorActivity(raw) {
  if (termPane !== 'agent') return; // shell output has no busy/idle markers
  const busy = /esc to interrupt/.test(raw);
  const idleAtPrompt = /shift\+tab to cycle/.test(raw) && !busy;
  if (busy) {
    executorBusy = true;
    idleStreak = 0;
    setTermState('● working…', 'busy');
  } else if (idleAtPrompt) {
    idleStreak++;
    if (executorBusy && idleStreak >= 2) {
      executorBusy = false;
      maybeAutoReload();
    }
    if (!executorBusy) setTermState('● live', 'live');
  }
}

// Agent / Shell pane tabs.
for (const btn of document.querySelectorAll('.term-tab')) {
  btn.addEventListener('click', () => {
    const pane = btn.dataset.pane;
    if (pane === termPane) return;
    termPane = pane;
    document.querySelectorAll('.term-tab').forEach((b) => b.classList.toggle('active', b === btn));
    executorBusy = false;
    if (termBoundTaskId != null) {
      attachTerminal();
    } else {
      showTermOverlay('Match a tab or pick a task to open its terminal.');
    }
  });
}

// --- Wiring --------------------------------------------------------------------

$('settings-btn').addEventListener('click', () => $('settings').classList.toggle('hidden'));

$('server-url').addEventListener('keydown', async (e) => {
  if (e.key !== 'Enter') return;
  await send({ type: 'setServerUrl', url: $('server-url').value.trim() });
  $('settings').classList.add('hidden');
  refresh();
});

$('refresh-tasks').addEventListener('click', loadCandidates);

$('task-select').addEventListener('change', async (e) => {
  const taskId = Number(e.target.value);
  if (!taskId || activeTabId == null) return;
  const r = await send({ type: 'pickTask', tabId: activeTabId, taskId });
  if (r.ok) {
    currentTask = r.task;
    renderTask();
    syncTerminal(true);
    bridgeLoop();
  }
});

$('annotate-btn').addEventListener('click', async () => {
  if (activeTabId == null) return;
  const r = await send({ type: 'startAnnotate', tabId: activeTabId });
  $('send-result').textContent = r.error || 'Select mode on — click an element on the page.';
});

$('send-btn').addEventListener('click', async () => {
  if (activeTabId == null) return;
  $('send-btn').disabled = true;
  const r = await send({
    type: 'sendAnnotations',
    tabId: activeTabId,
    instruction: $('instruction').value.trim(),
  });
  if (r.ok) {
    $('send-result').textContent = `Sent to task #${r.taskId}${r.nudged ? ' — executor nudged ✓' : ' (bundle saved; no live executor)'}`;
    $('instruction').value = '';
    updateCount(0);
  } else {
    $('send-result').textContent = r.error || 'Send failed';
    $('send-btn').disabled = false;
  }
});

chrome.runtime.onMessage.addListener((msg) => {
  if (msg?.type === 'ty-annotations-count') {
    updateCount(msg.count);
  }
});

// --- Browser bridge: while the panel is open and a task is matched, long-poll
// ty serve for executor commands and run them against the live browser window.
// This is what lets the executor see/drive the page — and any other tab in the
// window it opens — without its own browser.
//
// The loop pins to the (task, tab) it started on. It deliberately does NOT
// follow the panel's active tab: the executor can bring a docs tab or a newly
// opened external tab to the foreground (e.g. to screenshot it) without the
// active-tab change tearing the bridge down. It stops when the panel is hidden
// or the pinned tab is closed.
let bridgeActive = false;
let bridgeTask = null; // the task the running bridge is pinned to

async function updateBridgeStatus(active, primaryTabId) {
  const el = $('bridge-status');
  if (!el) return;
  if (!active) {
    el.classList.add('hidden');
    el.textContent = '';
    return;
  }
  let count = 1;
  try {
    const primary = await chrome.tabs.get(primaryTabId);
    if (primary.groupId != null && primary.groupId !== -1) {
      count = (await chrome.tabs.query({ groupId: primary.groupId })).length;
    }
  } catch {}
  el.textContent = count > 1 ? `🔗 driving ${count} tabs` : '🔗 executor can drive this tab';
  el.classList.remove('hidden');
}

async function bridgeLoop() {
  if (bridgeActive) return;
  const task = currentTask;
  const taskId = task?.id;
  const primaryTabId = activeTabId;
  if (taskId == null || primaryTabId == null) return;
  bridgeActive = true;
  bridgeTask = task;
  updateBridgeStatus(true, primaryTabId);
  try {
    // Early console-tap injection (so logs accumulate before the executor asks)
    // and pin the tab→task match so external navigation doesn't drop the task.
    await send({ type: 'ensureBridge', tabId: primaryTabId, taskId });
    while (document.visibilityState === 'visible') {
      try {
        await chrome.tabs.get(primaryTabId); // pinned tab closed → stop
      } catch {
        break;
      }
      const r = await send({ type: 'browserPoll', taskId });
      if (r?.command) {
        const result = await send({ type: 'browserExec', tabId: primaryTabId, command: r.command });
        await send({ type: 'browserResult', taskId, id: r.command.id, result: result ?? { error: 'no result' } });
        updateBridgeStatus(true, primaryTabId);
      } else if (r?.error) {
        await new Promise((res) => setTimeout(res, 3000));
      }
    }
  } finally {
    bridgeActive = false;
    bridgeTask = null;
    updateBridgeStatus(false);
  }
}

// Auto-reload preference
chrome.storage.local.get('autoReload').then(({ autoReload }) => {
  if (autoReload === false) $('auto-reload').checked = false;
});
$('auto-reload').addEventListener('change', (e) => {
  chrome.storage.local.set({ autoReload: e.target.checked });
});

chrome.tabs.onActivated.addListener(refresh);
chrome.tabs.onUpdated.addListener((tabId, info) => {
  if (tabId === activeTabId && info.status === 'complete') refresh();
});
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') refresh();
});

refresh();
