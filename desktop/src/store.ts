import { useSyncExternalStore } from "react";
import { toast as sonnerToast } from "sonner";
import { api } from "./api/client";
import { subscribeBoard } from "./api/sse";
import type { ExecutorInfo, LogLine, Project, Task, TaskType } from "./api/types";
import { notify } from "./tauri";

export type View =
  | { kind: "board" }
  | { kind: "detail"; taskId: number }
  | { kind: "settings" };

export type Dialog =
  | { kind: "confirm"; title: string; message: string; danger?: boolean; onConfirm: () => void }
  | { kind: "retry"; taskId: number }
  | { kind: "status"; taskId: number }
  | { kind: "help" }
  | null;

export type FormState =
  | { kind: "new"; initialProject?: string }
  | { kind: "edit"; taskId: number }
  | null;

export interface Toast {
  taskId?: number;
  title: string;
  body?: string;
  kind: "info" | "success" | "warning" | "error";
}

export type PermissionMode = "" | "auto" | "dangerous";

export type ThemePreference = "system" | "light" | "dark";

export interface AppState {
  booted: boolean;
  bootError: string | null;
  tasks: Task[];
  projects: Project[];
  types: TaskType[];
  executors: ExecutorInfo[];
  latestLogs: Record<string, LogLine>;
  view: View;
  selectedTaskId: number | null;
  filter: string;
  filterOpen: boolean;
  collapsed: { backlog: boolean; done: boolean };
  permissionMode: PermissionMode;
  theme: ThemePreference;
  dialog: Dialog;
  form: FormState;
  paletteOpen: boolean;
  lastNotificationTaskId: number | null;
}

type Listener = () => void;

class Store {
  private state: AppState = {
    booted: false,
    bootError: null,
    tasks: [],
    projects: [],
    types: [],
    executors: [],
    latestLogs: {},
    view: { kind: "board" },
    selectedTaskId: null,
    filter: "",
    filterOpen: false,
    collapsed: { backlog: false, done: false },
    permissionMode: "",
    theme: (localStorage.getItem("theme") as ThemePreference) || "system",
    dialog: null,
    form: null,
    paletteOpen: false,
    lastNotificationTaskId: null,
  };

  private listeners = new Set<Listener>();
  private prevStatuses = new Map<number, string>();
  private refreshTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribeBoard: (() => void) | null = null;

  getState = (): AppState => this.state;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  private set(partial: Partial<AppState>) {
    this.state = { ...this.state, ...partial };
    for (const listener of this.listeners) listener();
  }

  // --- Boot & data loading ---

  async boot() {
    try {
      await this.loadAll();
      this.set({ booted: true, bootError: null });
      this.unsubscribeBoard?.();
      this.unsubscribeBoard = subscribeBoard(() => this.scheduleRefresh());
    } catch (e) {
      this.set({ bootError: e instanceof Error ? e.message : String(e) });
      throw e;
    }
  }

  async loadAll() {
    const [tasks, projects, types, executors] = await Promise.all([
      api.listTasks({ all: true }),
      api.listProjects(),
      api.listTypes(),
      api.listExecutors().catch(() => [] as ExecutorInfo[]),
    ]);
    this.detectTransitions(tasks);
    this.set({ tasks, projects, types, executors });
    void this.refreshActivity(tasks);
  }

  /** Debounced refresh used by the SSE change signal. */
  scheduleRefresh() {
    if (this.refreshTimer) return;
    this.refreshTimer = setTimeout(async () => {
      this.refreshTimer = null;
      try {
        await this.refreshTasks();
      } catch {
        // transient; SSE will fire again
      }
    }, 200);
  }

  async refreshTasks() {
    const tasks = await api.listTasks({ all: true });
    this.detectTransitions(tasks);
    this.set({ tasks });
    void this.refreshActivity(tasks);
  }

  private async refreshActivity(tasks: Task[]) {
    const active = tasks.filter((t) => t.status === "processing" || t.status === "blocked");
    if (active.length === 0) {
      if (Object.keys(this.state.latestLogs).length) this.set({ latestLogs: {} });
      return;
    }
    try {
      const latestLogs = await api.latestLogs(active.map((t) => t.id));
      this.set({ latestLogs });
    } catch {
      // sub-lines are cosmetic
    }
  }

  /** Toast + native notification on meaningful status transitions. */
  private detectTransitions(tasks: Task[]) {
    const isFirstLoad = this.prevStatuses.size === 0 && this.state.tasks.length === 0;
    for (const task of tasks) {
      const prev = this.prevStatuses.get(task.id);
      if (!isFirstLoad && prev && prev !== task.status) {
        if (task.status === "blocked") {
          this.toast({
            taskId: task.id,
            title: `#${task.id} needs input`,
            body: task.title,
            kind: "warning",
          });
          void notify(`Task #${task.id} needs input`, task.title);
          this.set({ lastNotificationTaskId: task.id });
        } else if (task.status === "done") {
          this.toast({
            taskId: task.id,
            title: `#${task.id} done`,
            body: task.title,
            kind: "success",
          });
          void notify(`Task #${task.id} done`, task.title);
          this.set({ lastNotificationTaskId: task.id });
        }
      }
      this.prevStatuses.set(task.id, task.status);
    }
  }

  // --- Navigation ---

  openBoard() {
    this.set({ view: { kind: "board" } });
  }

  openDetail(taskId: number) {
    this.set({ view: { kind: "detail", taskId }, selectedTaskId: taskId });
  }

  openSettings() {
    this.set({ view: { kind: "settings" } });
  }

  selectTask(taskId: number | null) {
    this.set({ selectedTaskId: taskId });
  }

  setFilter(filter: string) {
    this.set({ filter });
  }

  setFilterOpen(open: boolean) {
    this.set({ filterOpen: open });
  }

  toggleCollapsed(column: "backlog" | "done") {
    this.set({
      collapsed: { ...this.state.collapsed, [column]: !this.state.collapsed[column] },
    });
  }

  setPalette(open: boolean) {
    this.set({ paletteOpen: open });
  }

  setDialog(dialog: Dialog) {
    this.set({ dialog });
  }

  setForm(form: FormState) {
    this.set({ form });
  }

  cyclePermissionMode() {
    const order: PermissionMode[] = ["", "auto", "dangerous"];
    const next = order[(order.indexOf(this.state.permissionMode) + 1) % order.length];
    this.set({ permissionMode: next });
    this.toast({
      title: `Permission mode: ${next === "" ? "default" : next}`,
      kind: next === "dangerous" ? "warning" : "info",
    });
  }

  setTheme(theme: ThemePreference) {
    localStorage.setItem("theme", theme);
    this.set({ theme });
  }

  // --- Toasts (sonner) ---

  toast(toast: Toast) {
    const fn =
      toast.kind === "success"
        ? sonnerToast.success
        : toast.kind === "warning"
          ? sonnerToast.warning
          : toast.kind === "error"
            ? sonnerToast.error
            : sonnerToast.info;
    fn(toast.title, {
      description: toast.body,
      duration: toast.kind === "warning" || toast.kind === "error" ? 10000 : 5000,
      action: toast.taskId
        ? { label: "Open", onClick: () => this.openDetail(toast.taskId!) }
        : undefined,
    });
  }

  // --- Task mutations (optimistic + refresh) ---

  private optimisticStatus(id: number, status: Task["status"]) {
    this.set({
      tasks: this.state.tasks.map((t) => (t.id === id ? { ...t, status } : t)),
    });
  }

  private async mutate(action: () => Promise<unknown>, errorTitle: string) {
    try {
      await action();
      await this.refreshTasks();
    } catch (e) {
      this.toast({
        title: errorTitle,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  executeTask(id: number, dangerous = false) {
    this.optimisticStatus(id, "queued");
    return this.mutate(async () => {
      const mode = dangerous ? "dangerous" : this.state.permissionMode;
      if (mode) {
        await api.updateTask(id, { permission_mode: mode });
      }
      await api.executeTask(id);
    }, `Failed to execute #${id}`);
  }

  closeTask(id: number) {
    this.optimisticStatus(id, "done");
    return this.mutate(() => api.closeTask(id), `Failed to close #${id}`);
  }

  archiveTask(id: number) {
    this.optimisticStatus(id, "archived");
    return this.mutate(() => api.setStatus(id, "archived"), `Failed to archive #${id}`);
  }

  deleteTask(id: number) {
    return this.mutate(() => api.deleteTask(id), `Failed to delete #${id}`);
  }

  pinTask(id: number) {
    return this.mutate(() => api.pinTask(id), `Failed to pin #${id}`);
  }

  retryTask(id: number, feedback: string) {
    return this.mutate(() => api.retryTask(id, feedback), `Failed to retry #${id}`);
  }

  setTaskStatus(id: number, status: string) {
    this.optimisticStatus(id, status as Task["status"]);
    return this.mutate(() => api.setStatus(id, status), `Failed to set status on #${id}`);
  }

  /** Drag-and-drop a card onto a column. Columns map to actions: In Progress
   * queues the task for execution, Done closes it, others set the status. */
  moveTaskToColumn(id: number, column: string) {
    const task = this.state.tasks.find((t) => t.id === id);
    if (!task) return;
    switch (column) {
      case "processing":
        if (task.status !== "processing" && task.status !== "queued") {
          void this.executeTask(id);
        }
        break;
      case "done":
        if (task.status !== "done") void this.closeTask(id);
        break;
      case "backlog":
        if (task.status !== "backlog") void this.setTaskStatus(id, "backlog");
        break;
      case "blocked":
        if (task.status !== "blocked") void this.setTaskStatus(id, "blocked");
        break;
    }
  }
}

export const store = new Store();

export function useAppState(): AppState {
  return useSyncExternalStore(store.subscribe, store.getState);
}

/** Narrow subscription: re-renders only when the selected slice changes
 * (reference equality). Keeps hot components (board cards) off the global
 * re-render path. */
export function useAppSelector<T>(selector: (state: AppState) => T): T {
  return useSyncExternalStore(store.subscribe, () => selector(store.getState()));
}
