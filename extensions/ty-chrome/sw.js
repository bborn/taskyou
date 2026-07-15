// ty-chrome service worker: owns all taskyou daemon HTTP calls, matches
// browser tabs to tasks by dev-server port, and routes messages between the
// side panel and the content-script overlay.

const DEFAULT_SERVER = 'http://127.0.0.1:8080';

// tabId -> { task, manual } (manual = user picked from dropdown, wins over port match)
const matches = new Map();

// Chrome opens the panel on toolbar click (never disable this — it's what keeps
// the panel openable). We only *scope* it: after it opens on a tab we disable it
// on that window's other tabs, so it doesn't trail you onto unrelated tabs.
const PANEL_PATH = 'sidepanel.html';
chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch(() => {});

const panelScope = new Map(); // windowId -> { groupId, tabId }

function tabInScope(tab, scope) {
  if (!scope || !tab) return false;
  if (scope.groupId != null && scope.groupId !== -1 /* TAB_GROUP_NONE */) {
    return tab.groupId === scope.groupId;
  }
  return tab.id === scope.tabId;
}

// Enable the panel only on in-scope tabs of this window, disable it on the rest.
async function reconcileWindowPanel(windowId) {
  const scope = panelScope.get(windowId);
  if (!scope) return;
  let tabs = [];
  try {
    tabs = await chrome.tabs.query({ windowId });
  } catch {
    return;
  }
  for (const t of tabs) {
    chrome.sidePanel
      .setOptions({ tabId: t.id, path: PANEL_PATH, enabled: tabInScope(t, scope) })
      .catch(() => {});
  }
}

async function getServerUrl() {
  const { serverUrl } = await chrome.storage.local.get('serverUrl');
  return (serverUrl || DEFAULT_SERVER).replace(/\/+$/, '');
}

// Auto-discovery: when the configured URL isn't answering, probe common
// ty serve locations and adopt the first one that responds.
const CANDIDATE_SERVERS = [
  'http://127.0.0.1:8080',
  'http://localhost:8080',
  'http://127.0.0.1:8484', // TaskYou desktop app default port
  'http://127.0.0.1:8765', // isolated demo instance
];
let probePromise = null;
let lastFailedSweep = 0;

async function isAlive(base) {
  try {
    const res = await fetch(base + '/api/status', { signal: AbortSignal.timeout(800) });
    return res.ok;
  } catch {
    return false;
  }
}

// Concurrent callers share one in-flight sweep; the cooldown only applies
// after a sweep that found nothing (so a panel retry can't get a stale "no").
async function ensureServer() {
  const current = await getServerUrl();
  if (await isAlive(current)) return { serverUrl: current, connected: true };
  if (Date.now() - lastFailedSweep < 5_000) return { serverUrl: current, connected: false };
  if (!probePromise) {
    probePromise = (async () => {
      for (const candidate of CANDIDATE_SERVERS) {
        if (candidate !== current && (await isAlive(candidate))) {
          await chrome.storage.local.set({ serverUrl: candidate });
          matches.clear();
          return candidate;
        }
      }
      lastFailedSweep = Date.now();
      return null;
    })().finally(() => {
      probePromise = null;
    });
  }
  const found = await probePromise;
  return found ? { serverUrl: found, connected: true } : { serverUrl: current, connected: false };
}

async function api(path, opts) {
  const base = await getServerUrl();
  const res = await fetch(base + path, opts);
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try { msg = (await res.json()).error || msg; } catch {}
    const err = new Error(msg);
    err.status = res.status;
    throw err;
  }
  return res.json();
}

async function candidateTasks() {
  const [processing, blocked] = await Promise.all([
    api('/api/tasks?status=processing').catch(() => []),
    api('/api/tasks?status=blocked').catch(() => []),
  ]);
  return [...processing, ...blocked];
}

// A hostname is "loopback" if it's localhost/127.0.0.1 or ends in a
// TLD reserved for local use (.localhost, .test per RFC 6761 — these always
// resolve to the loopback interface). Multi-tenant local dev setups serve each
// tenant on its own subdomain (e.g. qa-brand-4801.influencekit.test:3143), so
// port-based task matching must recognize those, not just bare localhost.
function isLoopbackHost(hostname) {
  return (
    hostname === 'localhost' ||
    hostname === '127.0.0.1' ||
    hostname.endsWith('.localhost') ||
    hostname.endsWith('.test')
  );
}

function tabPort(url) {
  try {
    const u = new URL(url);
    if (!isLoopbackHost(u.hostname)) return null;
    return u.port ? Number(u.port) : null;
  } catch {
    return null;
  }
}

async function matchTab(tabId, url) {
  const existing = matches.get(tabId);
  if (existing?.manual) return existing.task;

  const port = tabPort(url);
  let task = null;
  if (port) {
    try {
      const tasks = await candidateTasks();
      task = tasks.find((t) => t.port === port) || null;
    } catch {
      task = null;
    }
  }
  if (task) {
    matches.set(tabId, { task, manual: false });
  } else if (!existing?.manual) {
    matches.delete(tabId);
  }
  setBadge(tabId, task);
  return task;
}

function setBadge(tabId, task) {
  // Badge is a match indicator only — task ids run 4+ digits and Chrome
  // badges fit ~4 chars. The id lives in the tooltip and the side panel.
  chrome.action.setBadgeText({ tabId, text: task ? '✓' : '' }).catch(() => {});
  chrome.action.setBadgeBackgroundColor({ tabId, color: '#d05010' }).catch(() => {});
  const title = task
    ? `TaskYou — this tab matches task #${task.id}: ${task.title}`
    : 'TaskYou Annotate';
  chrome.action.setTitle({ tabId, title }).catch(() => {});
}

// Vendored Floating UI (core must load before dom; both expose isolated-world
// globals consumed by content.js). Files run in array order in the same world.
const OVERLAY_FILES = [
  'vendor/floating-ui.core.umd.min.js',
  'vendor/floating-ui.dom.umd.min.js',
  'content.js',
];

async function ensureInjected(tabId) {
  await chrome.scripting.executeScript({ target: { tabId }, files: OVERLAY_FILES });
  await chrome.scripting.executeScript({ target: { tabId }, files: ['console-tap.js'], world: 'MAIN' });
}

// --- Browser bridge tab group ------------------------------------------------
// The "group" the executor can drive is a real Chrome tab group (labeled
// "ty #<id>", orange): the matched app tab plus any docs/issue-tracker tabs the
// executor opens. This is a visible, user-revocable boundary — the executor
// can't see or touch your other tabs, and you can drag a tab in/out of the
// group to grant/revoke. The group id is read from Chrome per-command, so it
// survives MV3 service-worker teardown; nothing is stored.

const TAB_GROUP_NONE = -1; // chrome.tabGroups.TAB_GROUP_ID_NONE

// Ensure the matched tab is in a ty tab group, creating one if it's ungrouped.
// If the tab is already in a group (the user's or ours), we adopt it rather
// than yanking the tab out of the user's grouping.
async function ensureTaskGroup(primaryTabId, taskId) {
  const tab = await chrome.tabs.get(primaryTabId);
  if (tab.groupId != null && tab.groupId !== TAB_GROUP_NONE) return tab.groupId;
  const groupId = await chrome.tabs.group({ tabIds: [primaryTabId] });
  try {
    await chrome.tabGroups.update(groupId, {
      title: taskId != null ? `ty #${taskId}` : 'ty',
      color: 'orange',
    });
  } catch {}
  return groupId;
}

async function groupOf(primaryTabId) {
  const tab = await chrome.tabs.get(primaryTabId);
  return tab.groupId ?? TAB_GROUP_NONE;
}

// Resolve which tab a command targets: the optional params.tab (must be a live
// tab in the same tab group as the primary), else the primary (matched) tab.
async function resolveTargetTab(primaryTabId, params) {
  const requested = params?.tab;
  if (requested == null) return primaryTabId;
  const target = Number(requested);
  if (!Number.isInteger(target)) throw new Error(`invalid tab id: ${requested}`);
  if (target === primaryTabId) return target;
  const [primary, tab] = await Promise.all([
    chrome.tabs.get(primaryTabId),
    chrome.tabs.get(target).catch(() => null),
  ]);
  if (!tab) throw new Error(`tab ${target} not found`);
  if (primary.groupId === TAB_GROUP_NONE || tab.groupId !== primary.groupId) {
    throw new Error(`tab ${target} is not in this task's tab group`);
  }
  return target;
}

// Only http/https can be driven; block file:, chrome:, javascript:, etc.
function normalizeNavUrl(raw) {
  let u;
  try {
    u = new URL(raw);
  } catch {
    throw new Error(`invalid url: ${raw}`);
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') {
    throw new Error(`unsupported url scheme "${u.protocol}" — only http/https`);
  }
  return u.href;
}

async function captureTab(targetTabId) {
  const tab = await chrome.tabs.get(targetTabId);
  // captureVisibleTab only sees the window's foreground tab, so bring the
  // target forward first (matches how the executor expects "look at tab X").
  if (!tab.active) {
    await chrome.tabs.update(targetTabId, { active: true });
    await chrome.windows.update(tab.windowId, { focused: true }).catch(() => {});
    await new Promise((r) => setTimeout(r, 250));
  }
  const win = (await chrome.tabs.get(targetTabId)).windowId;
  return chrome.tabs.captureVisibleTab(win, { format: 'png' });
}

chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  try {
    const tab = await chrome.tabs.get(tabId);
    if (tab.url) matchTab(tabId, tab.url);
  } catch {}
});

chrome.tabs.onUpdated.addListener((tabId, info, tab) => {
  if (info.status === 'complete' && tab.url) matchTab(tabId, tab.url);
});

chrome.tabs.onRemoved.addListener((tabId) => matches.delete(tabId));

// Switching tabs: show the panel only when the new tab is in scope.
chrome.tabs.onActivated.addListener(async ({ tabId, windowId }) => {
  const scope = panelScope.get(windowId);
  if (!scope) return;
  try {
    const tab = await chrome.tabs.get(tabId);
    chrome.sidePanel
      .setOptions({ tabId, path: PANEL_PATH, enabled: tabInScope(tab, scope) })
      .catch(() => {});
  } catch {}
});

// If the scoped tab joins a group (e.g. the executor drops it into "ty #<id>"),
// widen scope to that whole group so the panel follows it.
chrome.tabs.onUpdated.addListener((tabId, info, tab) => {
  if (info.groupId === undefined || !tab) return;
  const scope = panelScope.get(tab.windowId);
  if (!scope) return;
  if (tabId === scope.tabId && info.groupId !== TAB_GROUP_NONE) scope.groupId = info.groupId;
  reconcileWindowPanel(tab.windowId);
});

chrome.windows.onRemoved.addListener((windowId) => panelScope.delete(windowId));

async function resolveTask(tabId) {
  const existing = matches.get(tabId);
  if (existing) return existing.task;
  try {
    const tab = await chrome.tabs.get(tabId);
    return tab.url ? await matchTab(tabId, tab.url) : null;
  } catch {
    return null;
  }
}

async function collectFromTab(tabId) {
  try {
    return await chrome.tabs.sendMessage(tabId, { type: 'ty-collect' });
  } catch {
    return null;
  }
}

async function sendAnnotations(tabId, payload, instruction) {
  const task = await resolveTask(tabId);
  if (!task) return { error: 'No task matches this tab. Pick one in the side panel.' };

  if (!payload) payload = await collectFromTab(tabId);
  if (!payload || !payload.annotations?.length) {
    return { error: 'No annotations on this page yet.' };
  }
  if (instruction) payload.instruction = instruction;

  let screenshot = null;
  try {
    const tab = await chrome.tabs.get(tabId);
    screenshot = await chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
  } catch {
    screenshot = null;
  }

  try {
    const resp = await api(`/api/tasks/${task.id}/annotations`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...payload, screenshot }),
    });
    chrome.tabs.sendMessage(tabId, { type: 'ty-clear' }).catch(() => {});
    chrome.runtime.sendMessage({ type: 'ty-annotations-count', count: 0 }).catch(() => {});
    return { ok: true, nudged: resp.nudged, path: resp.path, taskId: task.id };
  } catch (e) {
    return { error: `Send failed: ${e.message}` };
  }
}

const handlers = {
  async getState({ tabId }) {
    const { serverUrl, connected } = await ensureServer();
    const task = tabId != null ? await resolveTask(tabId) : null;
    let annotationCount = 0;
    if (tabId != null) {
      try {
        const r = await chrome.tabs.sendMessage(tabId, { type: 'ty-get-count' });
        annotationCount = r?.count || 0;
      } catch {}
    }
    return { serverUrl, connected, task, annotationCount };
  },

  async setServerUrl({ url }) {
    await chrome.storage.local.set({ serverUrl: url.replace(/\/+$/, '') });
    matches.clear();
    return { ok: true };
  },

  // The panel reports the tab it opened on; scope it to that tab (or its group)
  // so it stops trailing onto unrelated tabs in the window.
  async panelOpened({ tabId }) {
    if (tabId == null) return { ok: false };
    try {
      const tab = await chrome.tabs.get(tabId);
      panelScope.set(tab.windowId, { groupId: tab.groupId ?? TAB_GROUP_NONE, tabId: tab.id });
      reconcileWindowPanel(tab.windowId);
    } catch {}
    return { ok: true };
  },

  async listCandidateTasks() {
    try {
      return { tasks: await candidateTasks() };
    } catch (e) {
      return { error: e.message, tasks: [] };
    }
  },

  async pickTask({ tabId, taskId }) {
    const tasks = await candidateTasks();
    const task = tasks.find((t) => t.id === taskId);
    if (!task) return { error: 'task not found' };
    matches.set(tabId, { task, manual: true });
    setBadge(tabId, task);
    return { ok: true, task };
  },

  // Projects the daemon knows about, for the "New task" repo picker.
  async listProjects() {
    try {
      return { projects: await api('/api/projects') };
    } catch (e) {
      return { error: e.message, projects: [] };
    }
  },

  // Create a task via the daemon, then bind it to this tab so the panel and
  // terminal attach immediately. A just-created task is queued (or backlog)
  // with no port yet, so the port-based matcher can't find it — pin it manually,
  // exactly like pickTask does for an existing task.
  async createTask({ tabId, title, body, project, execute }) {
    try {
      const task = await api('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, body, project, execute: !!execute }),
      });
      if (tabId != null) {
        matches.set(tabId, { task, manual: true });
        setBadge(tabId, task);
      }
      return { task };
    } catch (e) {
      return { error: e.message };
    }
  },

  async startAnnotate({ tabId }) {
    try {
      await chrome.scripting.executeScript({ target: { tabId }, files: OVERLAY_FILES });
      await chrome.tabs.sendMessage(tabId, { type: 'ty-enter-select' });
      return { ok: true };
    } catch (e) {
      return { error: `Cannot annotate this page: ${e.message}` };
    }
  },

  async sendAnnotations({ tabId, payload, instruction }, sender) {
    const id = tabId ?? sender.tab?.id;
    if (id == null) return { error: 'no tab' };
    return sendAnnotations(id, payload, instruction);
  },

  async getOutput({ taskId, lines }) {
    try {
      const r = await api(`/api/tasks/${taskId}/output?lines=${lines || 150}`);
      return { output: r.output };
    } catch (e) {
      // 400 = task has no executor pane (a stable fact); 410 = capture-pane failed
      // (usually transient — the pane is mid-move between tmux windows). Keep them
      // distinct so the panel can debounce 410s instead of flashing "pane gone".
      if (e.status === 400) return { noExecutor: true };
      if (e.status === 410) return { gone: true };
      return { error: e.message };
    }
  },

  async taskInput({ taskId, message }) {
    try {
      await api(`/api/tasks/${taskId}/input`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message, enter: true }),
      });
      return { ok: true };
    } catch (e) {
      return { error: e.message };
    }
  },

  // --- Embedded terminal: resolve pane/window info, start a session, or make
  // sure the workdir shell pane exists. The panel opens the WebSocket itself
  // (ws://<serverUrl>/api/tasks/{id}/terminal); these just proxy the JSON
  // endpoints through the SW so it stays the single owner of serverUrl. ---

  async terminalInfo({ taskId }) {
    try {
      return { info: await api(`/api/tasks/${taskId}/terminal-info`) };
    } catch (e) {
      return { error: e.message, status: e.status };
    }
  },

  async ensureSession({ taskId }) {
    try {
      return { info: await api(`/api/tasks/${taskId}/session`, { method: 'POST' }) };
    } catch (e) {
      return { error: e.message, status: e.status };
    }
  },

  async ensureShellPane({ taskId }) {
    try {
      return { info: await api(`/api/tasks/${taskId}/shell`, { method: 'POST' }) };
    } catch (e) {
      return { error: e.message, status: e.status };
    }
  },

  // The panel needs the resolved server URL to open the terminal WebSocket
  // directly (WebSockets can't go through the SW message channel).
  async getServerUrl() {
    const { serverUrl, connected } = await ensureServer();
    return { serverUrl, connected };
  },

  // --- Browser bridge: the panel polls ty serve for executor commands and
  // executes them against the user's live tab. ---

  async browserPoll({ taskId }) {
    const base = await getServerUrl();
    try {
      const res = await fetch(`${base}/api/tasks/${taskId}/browser/poll`, {
        signal: AbortSignal.timeout(25_000),
      });
      if (res.status === 204) return { none: true };
      if (!res.ok) return { error: `poll failed: HTTP ${res.status}` };
      return { command: await res.json() };
    } catch (e) {
      return { error: e.message };
    }
  },

  async browserResult({ taskId, id, result }) {
    try {
      await api(`/api/tasks/${taskId}/browser/result`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, result }),
      });
      return { ok: true };
    } catch (e) {
      return { error: e.message };
    }
  },

  async browserExec({ tabId, command }) {
    const params = command.params || {};
    try {
      switch (command.action) {
        case 'screenshot': {
          const target = await resolveTargetTab(tabId, params);
          return { data: await captureTab(target) };
        }
        case 'reload': {
          const target = await resolveTargetTab(tabId, params);
          await chrome.tabs.reload(target);
          return { ok: true, tab: target };
        }
        case 'navigate': {
          const url = normalizeNavUrl(params.url || '');
          const target = await resolveTargetTab(tabId, params);
          await chrome.tabs.update(target, { url });
          return { ok: true, url, tab: target };
        }

        // --- Tab-group control (drive more than the matched tab) ---
        case 'tabs': {
          const primary = await chrome.tabs.get(tabId);
          const members =
            primary.groupId === TAB_GROUP_NONE
              ? [primary]
              : await chrome.tabs.query({ groupId: primary.groupId });
          return {
            tabs: members.map((t) => ({
              tab: t.id,
              url: t.url,
              title: t.title,
              active: t.active,
              primary: t.id === tabId,
            })),
          };
        }
        case 'open':
        case 'open_tab': {
          const url = normalizeNavUrl(params.url || '');
          const primary = await chrome.tabs.get(tabId);
          const tab = await chrome.tabs.create({
            windowId: primary.windowId,
            url,
            active: params.activate !== false,
          });
          // Add the new tab to the task's group so it's inside the boundary.
          const groupId = await ensureTaskGroup(tabId);
          await chrome.tabs.group({ groupId, tabIds: [tab.id] }).catch(() => {});
          return { ok: true, tab: tab.id, url };
        }
        case 'activate':
        case 'switch_tab': {
          const target = await resolveTargetTab(tabId, params);
          const tab = await chrome.tabs.get(target);
          await chrome.tabs.update(target, { active: true });
          await chrome.windows.update(tab.windowId, { focused: true }).catch(() => {});
          return { ok: true, tab: target };
        }
        case 'close':
        case 'close_tab': {
          const target = await resolveTargetTab(tabId, params);
          await chrome.tabs.remove(target);
          return { ok: true, closed: target };
        }

        case 'elements':
        case 'snapshot':
        case 'click':
        case 'type':
        case 'console': {
          const target = await resolveTargetTab(tabId, params);
          await ensureInjected(target);
          return await chrome.tabs.sendMessage(target, {
            type: 'ty-cmd',
            action: command.action,
            params,
          });
        }
        default:
          return { error: 'unknown action: ' + command.action };
      }
    } catch (e) {
      return { error: e.message };
    }
  },

  // Inject the bridge into the matched tab, put it in a visible "ty #<id>" tab
  // group (the boundary the executor can drive), and pin the tab→task match so
  // the executor can navigate it to an external site without the port-based
  // match dropping the task (which would tear the bridge down mid-session).
  async ensureBridge({ tabId, taskId }) {
    try {
      await ensureInjected(tabId);
      await ensureTaskGroup(tabId, taskId).catch(() => {});
      if (taskId != null) {
        const task = (await candidateTasks().catch(() => [])).find((t) => t.id === taskId);
        if (task) {
          matches.set(tabId, { task, manual: true });
          setBadge(tabId, task);
        }
      }
      return { ok: true };
    } catch (e) {
      return { error: e.message };
    }
  },

  async annotationsChanged({ count }, sender) {
    chrome.runtime.sendMessage({ type: 'ty-annotations-count', count, tabId: sender.tab?.id }).catch(() => {});
    return { ok: true };
  },
};

chrome.commands.onCommand.addListener(async (command) => {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id) return;
  if (command === 'annotate') {
    await handlers.startAnnotate({ tabId: tab.id });
  } else if (command === 'send-annotations') {
    const r = await sendAnnotations(tab.id, null, '');
    if (r.error) chrome.tabs.sendMessage(tab.id, { type: 'ty-toast', message: r.error }).catch(() => {});
  }
});

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  const handler = handlers[msg?.type];
  if (!handler) return false;
  handler(msg, sender)
    .then(sendResponse)
    .catch((e) => sendResponse({ error: e.message }));
  return true; // async
});
