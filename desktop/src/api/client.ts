import type {
  Attachment,
  Routine,
  RoutineRun,
  Dependencies,
  ExecutorInfo,
  LogLine,
  Project,
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
    try {
      const data = await res.json();
      if (data?.error) message = data.error;
    } catch {
      // non-JSON error body
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  // Tasks
  listTasks: (opts?: { all?: boolean; project?: string; limit?: number }) => {
    const params = new URLSearchParams();
    if (opts?.all) params.set("all", "true");
    if (opts?.project) params.set("project", opts.project);
    params.set("limit", String(opts?.limit ?? 1000));
    return request<Task[]>("GET", `/api/tasks?${params}`);
  },
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
  sendInput: (id: number, message: string) =>
    request<{ ok: boolean }>("POST", `/api/tasks/${id}/input`, { message, enter: true }),
  taskLogs: (id: number, limit = 200) => request<LogLine[]>("GET", `/api/tasks/${id}/logs?limit=${limit}`),
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

  status: () => request<{ status: string; tasks: Record<string, number> }>("GET", "/api/status"),
};

export { ApiError };
