import type {
  Attachment,
  CatalogPlugin,
  InstalledPlugin,
  PluginCatalog,
  ChatMessage,
  Placement,
  PlacementHost,
  Routine,
  RoutineRun,
  Dependencies,
  ExecutorInfo,
  LogLine,
  Project,
  SavedView,
  SavedViewResult,
  Task,
  TaskDetail,
  TaskType,
  TerminalInfo,
} from "./types";

// Default API base: when the production bundle is served by `ty serve` itself,
// the API is same-origin; in vite dev and in the Tauri shell (tauri://) we
// start from the standard local port — the desktop supervisor overrides it
// with the configured port during boot anyway.
let baseUrl =
  !import.meta.env.DEV && typeof window !== "undefined" && window.location.protocol.startsWith("http")
    ? window.location.origin
    : "http://127.0.0.1:8484";

export function setApiBase(url: string) {
  baseUrl = url.replace(/\/$/, "");
}

export function apiBase(): string {
  return baseUrl;
}

class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    /** Stable machine-readable code from the API, e.g. "agent_busy". */
    public code?: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = `${method} ${path} failed (${res.status})`;
    let code: string | undefined;
    try {
      const data = await res.json();
      if (data?.error) message = data.error;
      if (typeof data?.code === "string") code = data.code;
    } catch {
      // non-JSON error body
    }
    throw new ApiError(res.status, message, code);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
 placement: (id: number) => request<Placement>("GET", `/api/tasks/${id}/placement`),
 placeTask: (id: number, target: string, workdir: string) => request<{messages: string[]}>("POST", `/api/tasks/${id}/placement`, {target, workdir}),
  // The machines a new task in this project could be placed on. An empty list
  // means nothing is offering a choice, and the form falls back to automatic
  // placement without showing a picker.
  placementHosts: (project: string, executor?: string) => {
    const params = new URLSearchParams({ project });
    if (executor) params.set("executor", executor);
    return request<{ hosts: PlacementHost[] }>("GET", `/api/placement/hosts?${params}`);
  },
  // Tasks
  // `filter` runs the shared query grammar (internal/taskfilter) server-side.
  // Filtering goes through it rather than a second parser in the browser,
  // because two parsers are two sets of answers for the same string.
  listTasks: (opts?: { all?: boolean; project?: string; limit?: number; filter?: string }) => {
    const params = new URLSearchParams();
    if (opts?.all) params.set("all", "true");
    if (opts?.project) params.set("project", opts.project);
    if (opts?.filter) params.set("filter", opts.filter);
    params.set("limit", String(opts?.limit ?? 1000));
    return request<Task[]>("GET", `/api/tasks?${params}`);
  },
  taskLogsBefore: (id: number, before: number, limit = 200) =>
    request<LogLine[]>("GET", `/api/tasks/${id}/logs?before=${before}&limit=${limit}`),
  taskDetail: (id: number) => request<TaskDetail>("GET", `/api/tasks/${id}`),
  createTask: (task: {
    title: string;
    body: string;
    type: string;
    project: string;
    executor: string;
    execute: boolean;
    pinned?: boolean;
    permission_mode?: string;
    tags?: string;
    // A host chosen by hand instead of by the resolver: "" leaves the choice to
    // it, "local" pins the task to this machine, anything else is a destination
    // from placementHosts().
    placement?: string;
    placement_workdir?: string;
  }) => request<Task>("POST", "/api/tasks", task),
  updateTask: (
    id: number,
    patch: Partial<
      Pick<
        Task,
        | "title"
        | "body"
        | "type"
        | "project"
        | "executor"
        | "tags"
        | "pinned"
        | "permission_mode"
        | "effort_level"
      >
    >,
  ) => request<Task>("PATCH", `/api/tasks/${id}`, patch),
  deleteTask: (id: number) => request<{ ok: boolean }>("DELETE", `/api/tasks/${id}`),
  setStatus: (id: number, status: string) =>
    request<{ ok: boolean }>("POST", `/api/tasks/${id}/status`, { status }),
  executeTask: (id: number) => request<{ ok: boolean }>("POST", `/api/tasks/${id}/execute`, {}),
  closeTask: (id: number) => request<{ ok: boolean }>("POST", `/api/tasks/${id}/close`, {}),
  retryTask: (id: number, feedback: string) =>
    request<{ ok: boolean }>("POST", `/api/tasks/${id}/retry`, { feedback }),
  pinTask: (id: number) => request<{ pinned: boolean }>("POST", `/api/tasks/${id}/pin`, { toggle: true }),
  // force types the message even while the agent is working. Without it the API
  // refuses with code "agent_busy" rather than dropping text into the middle of
  // the agent's own output.
  sendInput: (id: number, message: string, force = false, attachmentIds: number[] = []) =>
    request<{ ok: boolean }>("POST", `/api/tasks/${id}/input`, { message, enter: true, force, attachment_ids: attachmentIds }),
  taskLogs: (id: number, limit = 200) => request<LogLine[]>("GET", `/api/tasks/${id}/logs?limit=${limit}`),
  // 60 turns is a long scroll on a phone and ~50KB instead of ~175KB; older
  // history is a deliberate request, not something to ship on every poll.
  taskMessages: (id: number, limit = 60) =>
    request<ChatMessage[]>("GET", `/api/tasks/${id}/messages?limit=${limit}`),
  latestLogs: (ids: number[]) =>
    request<Record<string, LogLine>>("GET", `/api/tasks/latest-logs?ids=${ids.join(",")}`),

  // Dependencies
  deps: (id: number) => request<Dependencies>("GET", `/api/tasks/${id}/deps`),
  addBlocker: (id: number, blockerId: number) =>
    request<{ ok: boolean }>("POST", `/api/tasks/${id}/block`, { blocker_id: blockerId }),
  removeBlocker: (id: number, blockerId: number) =>
    request<{ ok: boolean }>("POST", `/api/tasks/${id}/unblock`, { blocker_id: blockerId }),

  // Terminal
  terminalInfo: (id: number) => request<TerminalInfo>("GET", `/api/tasks/${id}/terminal-info`),
  ensureSession: (id: number) => request<TerminalInfo>("POST", `/api/tasks/${id}/session`, {}),
  ensureShellPane: (id: number) => request<TerminalInfo>("POST", `/api/tasks/${id}/shell`, {}),

  // Attachments
  listAttachments: (taskId: number) => request<Attachment[]>("GET", `/api/tasks/${taskId}/attachments`),
  addAttachment: (taskId: number, filename: string, dataBase64: string, mimeType?: string) =>
    request<Attachment>("POST", `/api/tasks/${taskId}/attachments`, {
      filename,
      data: dataBase64,
      mime_type: mimeType,
    }),
  deleteAttachment: (id: number) => request<{ ok: boolean }>("DELETE", `/api/attachments/${id}`),
  attachmentUrl: (id: number) => `${baseUrl}/api/attachments/${id}`,

  // Projects
  listProjects: () => request<Project[]>("GET", "/api/projects"),
  createProject: (p: Partial<Project>) => request<Project>("POST", "/api/projects", p),
  updateProject: (name: string, p: Partial<Project>) =>
    request<Project>("PATCH", `/api/projects/${encodeURIComponent(name)}`, p),
  deleteProject: (name: string) =>
    request<{ ok: boolean }>("DELETE", `/api/projects/${encodeURIComponent(name)}`),

  // Task types
  listTypes: () => request<TaskType[]>("GET", "/api/types"),
  createType: (t: Partial<TaskType>) => request<TaskType>("POST", "/api/types", t),
  updateType: (name: string, t: Partial<TaskType>) =>
    request<TaskType>("PATCH", `/api/types/${encodeURIComponent(name)}`, t),
  deleteType: (name: string) =>
    request<{ ok: boolean }>("DELETE", `/api/types/${encodeURIComponent(name)}`),

  // Executors / settings / autocomplete
  listExecutors: () => request<ExecutorInfo[]>("GET", "/api/executors"),
  // Saved views — named filter queries, resolved server-side so the query
  // grammar has exactly one implementation.
  listViews: () => request<SavedView[]>("GET", "/api/views"),
  getView: (name: string) =>
    request<SavedViewResult>("GET", `/api/views/${encodeURIComponent(name)}`),
  saveView: (name: string, query: string) =>
    request<SavedView>("POST", "/api/views", { name, query }),
  deleteView: (name: string) =>
    request<{ ok: boolean }>("DELETE", `/api/views/${encodeURIComponent(name)}`),

  getSettings: () => request<Record<string, string>>("GET", "/api/settings"),
  updateSettings: (patch: Record<string, string>) =>
    request<{ ok: boolean }>("PATCH", "/api/settings", patch),
  autocomplete: (input: string, fieldType: "title" | "body", project: string, context = "") =>
    request<{ suggestion: string; full_text: string }>("POST", "/api/autocomplete", {
      input,
      field_type: fieldType,
      project,
      context,
    }),

  // Routines
  listRoutines: () => request<Routine[]>("GET", "/api/routines"),
  routineRuns: (name: string, limit = 20) =>
    request<RoutineRun[]>("GET", `/api/routines/${encodeURIComponent(name)}/runs?limit=${limit}`),
  routineRunLog: (name: string, runId: number) =>
    request<{ log: string; note?: string }>(
      "GET",
      `/api/routines/${encodeURIComponent(name)}/runs/${runId}/log`,
    ),
  runRoutine: (name: string) =>
    request<{ started: boolean }>("POST", `/api/routines/${encodeURIComponent(name)}/run`, {}),

  // Plugins
  listPlugins: () => request<InstalledPlugin[]>("GET", "/api/plugins"),
  pluginCatalog: (opts: { q?: string; scope?: string; refresh?: boolean } = {}) => {
    const params = new URLSearchParams();
    if (opts.q) params.set("q", opts.q);
    if (opts.scope && opts.scope !== "all") params.set("scope", opts.scope);
    if (opts.refresh) params.set("refresh", "1");
    const qs = params.toString();
    return request<PluginCatalog>("GET", `/api/plugins/catalog${qs ? `?${qs}` : ""}`);
  },
  installPlugin: (plugin: Pick<CatalogPlugin, "id"> | { source: string; subdir?: string; name?: string }) =>
    request<{ ok: boolean; updated?: boolean; plugins?: string[]; error?: string }>(
      "POST",
      "/api/plugins/install",
      plugin,
    ),
  removePlugin: (name: string) =>
    request<{ ok: boolean; dir?: string; in_collection_checkout?: boolean; error?: string }>(
      "POST",
      "/api/plugins/remove",
      { name },
    ),

  status: () => request<{ status: string; tasks: Record<string, number> }>("GET", "/api/status"),
};

export { ApiError };
