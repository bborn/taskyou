// ty-chrome service worker: owns all taskyou daemon HTTP calls, matches
// browser tabs to tasks by dev-server port, and routes messages between the
// side panel and the content-script overlay.

const DEFAULT_SERVER = 'http://127.0.0.1:8080';

// tabId -> { task, manual } (manual = user picked from dropdown, wins over port match)
const matches = new Map();

chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch(() => {});

async function getServerUrl() {
  const { serverUrl } = await chrome.storage.local.get('serverUrl');
  return (serverUrl || DEFAULT_SERVER).replace(/\/+$/, '');
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

function tabPort(url) {
  try {
    const u = new URL(url);
    if (u.hostname !== 'localhost' && u.hostname !== '127.0.0.1') return null;
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
  const text = task ? String(task.id) : '';
  chrome.action.setBadgeText({ tabId, text }).catch(() => {});
  chrome.action.setBadgeBackgroundColor({ tabId, color: '#0d9488' }).catch(() => {});
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
    const serverUrl = await getServerUrl();
    let connected = false;
    try {
      await api('/api/status');
      connected = true;
    } catch {}
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

  async startAnnotate({ tabId }) {
    try {
      await chrome.scripting.executeScript({ target: { tabId }, files: ['content.js'] });
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
      return e.status === 410 || e.status === 400 ? { gone: true } : { error: e.message };
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

  async annotationsChanged({ count }, sender) {
    chrome.runtime.sendMessage({ type: 'ty-annotations-count', count, tabId: sender.tab?.id }).catch(() => {});
    return { ok: true };
  },
};

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  const handler = handlers[msg?.type];
  if (!handler) return false;
  handler(msg, sender)
    .then(sendResponse)
    .catch((e) => sendResponse({ error: e.message }));
  return true; // async
});
