// ty-chrome side panel: shows the task matched to the active tab, sends
// annotations, and "teleports" the executor in — a live, polled view of the
// task's Claude pane plus a follow-up input line.

const $ = (id) => document.getElementById(id);

let activeTabId = null;
let currentTask = null;
let pollTimer = null;
let reconnectTimer = null;

const send = (msg) => chrome.runtime.sendMessage(msg);

const stripAnsi = (s) =>
  s
    .replace(/\x1b\[[0-9;?]*[ -\/]*[@-~]/g, '')
    .replace(/\x1b\][^\x07]*(\x07|\x1b\\)/g, '')
    .replace(/\x1b[=>]/g, '');

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
  schedulePolling();
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
    $('executor-state').textContent = currentTask.has_executor ? 'live' : 'no executor pane';
  } else {
    match.classList.add('hidden');
    picker.classList.remove('hidden');
    $('executor-state').textContent = '';
    $('console').textContent = '';
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

// --- Executor console polling --------------------------------------------------

// A 410 from the output endpoint usually means the executor pane is mid-move
// between tmux windows (join-pane during a dock toggle or daemon restart), not
// that it's actually gone. Ride out a few consecutive misses (~7.5s at the 2.5s
// cadence) before declaring "pane gone" so a transient capture failure doesn't
// flash, and re-fetch the task while gone so a recovered pane self-heals.
let goneStreak = 0;
const GONE_THRESHOLD = 3;

function schedulePolling() {
  clearInterval(pollTimer);
  goneStreak = 0;
  pollTimer = setInterval(poll, 2500);
  poll();
}

// Re-resolve the bound task so has_executor/status reflect reality after the
// pane moved or the daemon rebuilt the window. Only refresh fields (never switch
// task) so a manual pick isn't clobbered.
async function refreshTaskState() {
  const state = await send({ type: 'getState', tabId: activeTabId });
  if (state?.task && state.task.id === currentTask?.id) currentTask = state.task;
}

// Turn a raw tmux pane capture into a readable activity feed: drop the
// input-box chrome, prompt, footers, and tips; colorize diffs and actions.
function classifyLine(line) {
  const t = line.trim();
  if (/^\s*\d+\s*\+/.test(line) || /^\+/.test(t)) return 'x-add';
  if (/^\s*\d+\s*-/.test(line)) return 'x-del';
  if (/^⏺/.test(t)) return 'x-action';
  if (/^⎿/.test(t)) return 'x-meta';
  if (/^[✳✻✽✶✢✴✦∗*·]\s.*…/.test(t)) return 'x-status';
  return '';
}

function renderExecutor(raw, consoleEl) {
  const lines = stripAnsi(raw).split('\n');
  const frag = document.createDocumentFragment();
  let skipTip = false;
  let blanks = 0;
  for (const line of lines) {
    const t = line.trim();
    if (/^[─━]{6,}$/.test(t)) continue; // input box / separator rules
    if (/^[╭╰│]/.test(t)) continue; // box borders
    if (t === '❯' || /^❯\s*$/.test(t)) continue; // empty prompt
    if (/^⏵⏵/.test(t) || /shift\+tab to cycle/.test(t)) continue; // mode footer
    if (/Tip: /.test(t)) {
      skipTip = true;
      continue;
    }
    if (skipTip) {
      if (t === '') skipTip = false;
      continue;
    }
    if (t === '') {
      if (++blanks > 1) continue;
    } else {
      blanks = 0;
    }
    const div = document.createElement('div');
    div.className = 'x-line ' + classifyLine(line);
    div.textContent = line || ' ';
    frag.appendChild(div);
  }
  // Trim trailing blank lines
  while (frag.lastChild && frag.lastChild.textContent.trim() === '') frag.removeChild(frag.lastChild);
  consoleEl.replaceChildren(frag);
}

// Auto-reload: when the executor transitions from working to idle, its batch
// of edits is complete — reload the page so the user sees the result. Never
// reload while annotations are still pinned (unsent work on the page).
let executorBusy = false;
let idleStreak = 0;

function maybeAutoReload() {
  if (!$('auto-reload').checked || activeTabId == null) return;
  if (!$('annotation-count').classList.contains('zero')) return;
  chrome.tabs.reload(activeTabId);
  $('executor-state').textContent = '↻ page reloaded';
}

function trackExecutorActivity(raw) {
  const busy = /esc to interrupt/.test(raw);
  const idleAtPrompt = /shift\+tab to cycle/.test(raw) && !busy;
  if (busy) {
    executorBusy = true;
    idleStreak = 0;
    $('executor-state').textContent = 'working…';
  } else if (idleAtPrompt) {
    idleStreak++;
    if (executorBusy && idleStreak >= 2) {
      executorBusy = false;
      maybeAutoReload();
    }
    if (!executorBusy && $('executor-state').textContent === 'working…') {
      $('executor-state').textContent = 'live';
    }
  }
}

async function poll() {
  // Poll whenever a task is bound — don't gate on the cached has_executor flag,
  // which can be a stale false (captured while the pane id was briefly empty) and
  // would otherwise freeze the panel on a stale state with no way to recover.
  if (document.visibilityState !== 'visible' || !currentTask) return;
  const r = await send({ type: 'getOutput', taskId: currentTask.id, lines: 250 });
  const consoleEl = $('console');
  if (r.noExecutor) {
    // Stable fact (HTTP 400) — show it immediately, no debounce.
    goneStreak = 0;
    $('executor-state').textContent = 'no executor pane';
    return;
  }
  if (r.gone) {
    goneStreak++;
    if (goneStreak >= GONE_THRESHOLD) {
      $('executor-state').textContent = 'pane gone';
      // While persistently gone, re-fetch the task so a relocated/recovered pane
      // is picked up instead of staying frozen.
      if (goneStreak % GONE_THRESHOLD === 0) refreshTaskState();
    }
    return;
  }
  goneStreak = 0;
  if (r.output != null) {
    const atBottom = consoleEl.scrollHeight - consoleEl.scrollTop - consoleEl.clientHeight < 30;
    renderExecutor(r.output, consoleEl);
    if (atBottom) consoleEl.scrollTop = consoleEl.scrollHeight;
    trackExecutorActivity(r.output);
  }
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

async function sendFollowup() {
  const msg = $('followup').value.trim();
  if (!msg || !currentTask) return;
  const r = await send({ type: 'taskInput', taskId: currentTask.id, message: msg });
  if (r.ok) {
    $('followup').value = '';
    setTimeout(poll, 600);
  } else {
    $('executor-state').textContent = r.error || 'input failed';
  }
}
$('followup-send').addEventListener('click', sendFollowup);
$('followup').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') sendFollowup();
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
    count = (await chrome.tabs.query({ windowId: primary.windowId })).length;
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
