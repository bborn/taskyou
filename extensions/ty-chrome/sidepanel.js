// ty-chrome side panel: shows the task matched to the active tab, sends
// annotations, and "teleports" the executor in — a live, polled view of the
// task's Claude pane plus a follow-up input line.

const $ = (id) => document.getElementById(id);

let activeTabId = null;
let currentTask = null;
let pollTimer = null;

const send = (msg) => chrome.runtime.sendMessage(msg);

const stripAnsi = (s) =>
  s
    .replace(/\x1b\[[0-9;?]*[ -\/]*[@-~]/g, '')
    .replace(/\x1b\][^\x07]*(\x07|\x1b\\)/g, '')
    .replace(/\x1b[=>]/g, '');

// --- State refresh -----------------------------------------------------------

async function getActiveTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab || null;
}

async function refresh() {
  const tab = await getActiveTab();
  activeTabId = tab?.id ?? null;
  const state = await send({ type: 'getState', tabId: activeTabId });

  $('status-dot').classList.toggle('ok', !!state.connected);
  $('server-url').value = state.serverUrl;
  $('no-connection').classList.toggle('hidden', !!state.connected);

  currentTask = state.task || null;
  renderTask();
  $('annotation-count').textContent = `${state.annotationCount} annotation${state.annotationCount === 1 ? '' : 's'} pinned`;

  if (!currentTask && state.connected) loadCandidates();
  schedulePolling();
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

function schedulePolling() {
  clearInterval(pollTimer);
  pollTimer = setInterval(poll, 2500);
  poll();
}

async function poll() {
  if (document.visibilityState !== 'visible' || !currentTask?.has_executor) return;
  const r = await send({ type: 'getOutput', taskId: currentTask.id, lines: 150 });
  const consoleEl = $('console');
  if (r.gone) {
    $('executor-state').textContent = 'pane gone';
    return;
  }
  if (r.output != null) {
    const atBottom = consoleEl.scrollHeight - consoleEl.scrollTop - consoleEl.clientHeight < 30;
    consoleEl.textContent = stripAnsi(r.output).replace(/\n{3,}/g, '\n\n').trimEnd();
    if (atBottom) consoleEl.scrollTop = consoleEl.scrollHeight;
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
  $('send-btn').disabled = false;
  if (r.ok) {
    $('send-result').textContent = `Sent to task #${r.taskId}${r.nudged ? ' — executor nudged ✓' : ' (bundle saved; no live executor)'}`;
    $('instruction').value = '';
  } else {
    $('send-result').textContent = r.error || 'Send failed';
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
    $('annotation-count').textContent = `${msg.count} annotation${msg.count === 1 ? '' : 's'} pinned`;
  }
});

chrome.tabs.onActivated.addListener(refresh);
chrome.tabs.onUpdated.addListener((tabId, info) => {
  if (tabId === activeTabId && info.status === 'complete') refresh();
});
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') refresh();
});

refresh();
