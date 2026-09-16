import { useSyncExternalStore } from "react";
import { toast as sonnerToast } from "sonner";
import { CoalescedRefresh } from "./lib/refresh";

/** Typing resolves against the server, so it waits for a pause first. The
 * server is local; this is about not firing a request per keystroke, not
 * latency. */
const FILTER_DEBOUNCE_MS = 150;
import { api } from "./api/client";
import { subscribeBoard } from "./api/sse";
import type { ExecutorInfo, LogLine, Project, SavedView, Task, TaskType } from "./api/types";
import { DEFAULT_LIST_OPTIONS, normalizeListOptions, type ListOptions } from "./lib/list";
import { notify } from "./tauri";

export type View =
  | { kind: "board" }
  | { kind: "detail"; taskId: number }
  | { kind: "settings" }
  | { kind: "routines" };

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

/** Kanban columns, or one flat list. */
export type BoardMode = "board" | "list";

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
  boardMode: BoardMode;
  listOptions: ListOptions;
  savedViews: SavedView[];
  /** Name of the applied saved view, "" when the filter was typed by hand. */
  activeView: string;
  /** Ids the current filter matches, resolved by the server so the query
   * grammar is never reimplemented here. Null when no filter is set.
   *
   * Every filter goes through this — typed or from a saved view — because two
   * parsers are two sets of answers: `status:blocked` meant nothing to the
   * browser's own matcher, so the same string filtered one way from a view and
   * another way once you edited it. */
  filteredIds: Set<number> | null;
  arrangeOpen: boolean;
  viewsOpen: boolean;
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
    boardMode: "board",
    listOptions: DEFAULT_LIST_OPTIONS,
    savedViews: [],
    activeView: "",
    filteredIds: null,
    arrangeOpen: false,
    viewsOpen: false,
    collapsed: { backlog: false, done: false },
    permissionMode: "",
    theme: (localStorage.getItem("theme") as ThemePreference) || "system",
    dialog: null,
    form: null,
    paletteOpen: false,
    lastNotificationTaskId: null,
  };

  private listeners = new Set<Listener>();
  /** Guards against a slow response for an old filter landing after a fast one
   * for the current filter. Only the newest request may write filteredIds. */
  private filterSeq = 0;
  private filterTimer: ReturnType<typeof setTimeout> | null = null;
  private prevStatuses = new Map<number, string>();
  private refreshTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribeBoard: (() => void) | null = null;
  private taskRefresh = new CoalescedRefresh(async () => {
    const tasks = await api.listTasks({ all: true });
    this.detectTransitions(tasks);
    this.set({ tasks });
    await this.refreshActivity(tasks);
    // Membership is a function of task state, so it has to be recomputed when
    // task state changes — otherwise a task you just finished stays in "Active"
    // and one that just became blocked never appears until the view is
    // reapplied.
    await this.resolveFilter({ immediate: true });
  });

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
    const [tasks, projects, types, executors, settings, savedViews] = await Promise.all([
      api.listTasks({ all: true }),
      api.listProjects(),
      api.listTypes(),
      api.listExecutors().catch(() => [] as ExecutorInfo[]),
      api.getSettings().catch(() => ({}) as Record<string, string>),
      api.listViews().catch(() => [] as SavedView[]),
    ]);
    this.detectTransitions(tasks);
    this.set({
      tasks,
      projects,
      types,
      executors,
      savedViews,
      // The same settings keys the TUI writes, so the board you left in one is
      // the board you come back to in the other.
      boardMode: settings.board_display_mode === "list" ? "list" : "board",
      listOptions: normalizeListOptions({
        groupBy: settings.list_group_by as ListOptions["groupBy"],
        sort: settings.list_sort as ListOptions["sort"],
      }),
    });
    await this.refreshActivity(tasks);
    // A persisted view has to be re-resolved: its membership is the server's
    // answer, not something we can restore from a string.
    if (settings.board_view) {
      void this.applyView(settings.board_view);
    } else if (settings.board_filter) {
      this.set({ filter: settings.board_filter });
      await this.resolveFilter({ immediate: true });
    }
  }

  // --- List view, saved views, arrangement ---

  setBoardMode(mode: BoardMode) {
    this.set({ boardMode: mode });
    void api.updateSettings({ board_display_mode: mode }).catch(() => {});
  }

  toggleBoardMode() {
    this.setBoardMode(this.state.boardMode === "list" ? "board" : "list");
  }

  setListOptions(listOptions: ListOptions) {
    this.set({ listOptions });
    void api
      .updateSettings({ list_group_by: listOptions.groupBy, list_sort: listOptions.sort })
      .catch(() => {});
  }

  setArrangeOpen(arrangeOpen: boolean) {
    this.set({ arrangeOpen });
  }

  setViewsOpen(viewsOpen: boolean) {
    this.set({ viewsOpen });
  }

  async refreshViews() {
    try {
      this.set({ savedViews: await api.listViews() });
    } catch {
      // the picker shows what it has
    }
  }

  /** Apply a saved view: adopt its query and let the normal filter path resolve
   * it. A view is nothing but a named query, so it must not take a different
   * route to the answer than the same query typed by hand. */
  async applyView(name: string) {
    try {
      const view = await api.getView(name);
      this.set({ activeView: view.name, filter: view.query, viewsOpen: false });
      void api.updateSettings({ board_view: view.name, board_filter: view.query }).catch(() => {});
      await this.resolveFilter({ immediate: true });
    } catch (e) {
      this.toast({
        title: `Could not apply view "${name}"`,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  async saveCurrentAsView(name: string) {
    const query = this.state.filter.trim();
    if (!query) return;
    try {
      await api.saveView(name, query);
      await this.refreshViews();
      await this.applyView(name);
    } catch (e) {
      this.toast({
        title: `Could not save view "${name}"`,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  async deleteView(name: string) {
    try {
      await api.deleteView(name);
      if (this.state.activeView === name) this.clearFilter();
      await this.refreshViews();
    } catch (e) {
      this.toast({
        title: `Could not delete view "${name}"`,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  clearFilter() {
    this.set({ filter: "", activeView: "", filteredIds: null, filterOpen: false });
    this.filterSeq++; // abandon anything in flight
    void api.updateSettings({ board_view: "", board_filter: "" }).catch(() => {});
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

  refreshTasks() {
    return this.taskRefresh.request();
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
          notify(`Task #${task.id} needs input`, task.title);
          this.set({ lastNotificationTaskId: task.id });
        } else if (task.status === "done") {
          this.toast({
            taskId: task.id,
            title: `#${task.id} done`,
            body: task.title,
            kind: "success",
          });
          notify(`Task #${task.id} done`, task.title);
          this.set({ lastNotificationTaskId: task.id });
        }
      }
      this.prevStatuses.set(task.id, task.status);
    }
  }

  /** Resolve the current filter against the server and record what it matched.
   *
   * Typing debounces; anything else (applying a view, a task refresh) resolves
   * at once. The previous result is left in place while a new one is in flight,
   * so the list does not flash unfiltered mid-keystroke. */
  private async resolveFilter({ immediate = false } = {}): Promise<void> {
    if (this.filterTimer) {
      clearTimeout(this.filterTimer);
      this.filterTimer = null;
    }
    const query = this.state.filter.trim();
    if (query === "") {
      this.filterSeq++;
      this.set({ filteredIds: null });
      return;
    }
    if (!immediate) {
      await new Promise<void>((resolve) => {
        this.filterTimer = setTimeout(() => {
          this.filterTimer = null;
          resolve();
        }, FILTER_DEBOUNCE_MS);
      });
      // The filter may have moved on while we waited.
      if (this.state.filter.trim() !== query) return;
    }

    const seq = ++this.filterSeq;
    try {
      const matched = await api.listTasks({ all: true, filter: query });
      if (seq !== this.filterSeq) return; // a newer request owns the result
      this.set({ filteredIds: new Set(matched.map((t) => t.id)) });
    } catch {
      // Leave the previous result up; the next refresh or keystroke retries.
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

  openRoutines() {
    this.set({ view: { kind: "routines" } });
  }

  selectTask(taskId: number | null) {
    this.set({ selectedTaskId: taskId });
  }

  setFilter(filter: string) {
    // Typing over an applied view detaches the name — the view is a starting
    // point, not a lock — but NOT the way the query is answered. The same string
    // resolves through the same grammar whether it arrived from a view or from
    // the keyboard.
    this.set({ filter, activeView: "" });
    void api.updateSettings({ board_view: "", board_filter: filter }).catch(() => {});
    void this.resolveFilter();
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
