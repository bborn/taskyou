import { useEffect, useMemo, useRef, useState } from "react";
import { listen } from "@tauri-apps/api/event";
import { motion } from "motion/react";
import { Plus, Search, Settings2, ChevronLeft, Menu, Sun, Moon, MonitorSmartphone } from "lucide-react";
import logoUrl from "./assets/logo.png";
import { setApiBase } from "./api/client";
import { applyFilter, buildColumns } from "./lib/board";
import { buildSections, flattenSections } from "./lib/list";
import { store, useAppState } from "./store";
import { checkEnvironment, inTauri, openExternal, openInEditor, supervisorEnsure } from "./tauri";
import { Board } from "./components/Board";
import { MobileBoard } from "./components/MobileBoard";
import { MobileDrawer } from "./components/MobileDrawer";
import { useIsMobile } from "./hooks/use-mobile";
import { useVisualViewportShell } from "./hooks/use-keyboard-inset";
import { SetupCheck } from "./components/SetupCheck";
import { RoutinesView } from "./components/RoutinesView";
import { DetailView } from "./components/DetailView";
import { SettingsView } from "./components/SettingsView";
import { Palette } from "./components/Palette";
import { TaskForm } from "./components/TaskForm";
import { Dialogs } from "./components/Dialogs";
import { FilterBar } from "./components/FilterBar";
import { TaskList } from "./components/TaskList";
import { ArrangeMenu, ListToolbar, ViewsMenu } from "./components/ListMenus";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Toaster } from "@/components/ui/sonner";

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return (
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    tag === "SELECT" ||
    target.isContentEditable ||
    // xterm.js focuses a hidden textarea; class lives on ancestors
    target.closest(".terminal-host") !== null
  );
}

export default function App() {
  const state = useAppState();
  const isMobile = useIsMobile();
  const [drawerOpen, setDrawerOpen] = useState(false);
  // popstate handlers close over stale state, so the drawer's open-ness has to
  // be readable from inside one.
  const drawerOpenRef = useRef(false);
  drawerOpenRef.current = drawerOpen;
  // Resizes the whole shell to the visual viewport while the keyboard is up,
  // so every input inside it clears the keys — not just the reply composer.
  const shellRef = useRef<HTMLDivElement>(null);
  useVisualViewportShell(shellRef, isMobile);
  const [bootPhase, setBootPhase] = useState<"starting" | "setup" | "ready" | "error">("starting");
  const [bootMessage, setBootMessage] = useState("Starting TaskYou…");
  const [envReport, setEnvReport] = useState<import("./api/types").EnvironmentReport | null>(null);
  const setupSkippedRef = useRef(false);
  const bootedRef = useRef(false);

  // --- Boot: supervise sidecars, then load data ---
  useEffect(() => {
    if (bootedRef.current) return;
    bootedRef.current = true;
    void bootApp();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function bootApp() {
    try {
      if (inTauri()) {
        // First-run gate: tmux + an executor CLI are machine prerequisites.
        if (!setupSkippedRef.current) {
          const report = await checkEnvironment().catch(() => null);
          if (report && (!report.tmux || !report.executors.some((e) => e.path))) {
            setEnvReport(report);
            setBootPhase("setup");
            return;
          }
        }
        setBootMessage("Checking TaskYou server…");
        const status = await supervisorEnsure();
        setApiBase(`http://127.0.0.1:${status.port}`);
      }
      setBootMessage("Loading board…");
      await store.boot();
      setBootPhase("ready");
      // Deep link (a bookmark, a shared link, a notification tap, or a tab the
      // phone reloaded from scratch): /?task=123 or /?view=settings. The URL is
      // left intact — it's the source of truth for the view from here on.
      const params = new URLSearchParams(window.location.search);
      const deepTask = Number(params.get("task"));
      const deepView = params.get("view");
      if (Number.isInteger(deepTask) && deepTask > 0) store.openDetail(deepTask);
      else if (deepView === "settings") store.openSettings();
      else if (deepView === "routines") store.openRoutines();
    } catch (e) {
      setBootMessage(e instanceof Error ? e.message : String(e));
      setBootPhase("error");
    }
  }

  // --- Native menu events ---
  useEffect(() => {
    if (!inTauri()) return;
    const unlisten = listen<string>("menu", ({ payload }) => {
      switch (payload) {
        case "new-task":
          return void store.setForm({ kind: "new" });
        case "settings":
          return void store.openSettings();
        case "routines":
          return void store.openRoutines();
        case "board":
          return void store.openBoard();
        case "search":
          return void store.setPalette(true);
      }
    });
    return () => {
      void unlisten.then((fn) => fn());
    };
  }, []);

  // Vibrancy shows through only in the macOS shell; everywhere else the
  // page keeps an opaque background.
  useEffect(() => {
    if (inTauri() && /Mac/.test(navigator.userAgent)) {
      document.documentElement.style.background = "transparent";
    }
  }, []);

  // --- Theme: follow system or explicit preference; sync window chrome ---
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const resolved = state.theme === "system" ? (media.matches ? "dark" : "light") : state.theme;
      document.documentElement.classList.toggle("dark", resolved === "dark");
      if (inTauri()) {
        void import("@tauri-apps/api/window").then(({ getCurrentWindow }) =>
          getCurrentWindow()
            .setTheme(state.theme === "system" ? null : state.theme)
            .catch(() => {}),
        );
      }
    };
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [state.theme]);

  // --- View <-> URL ---
  // In-app navigation used to leave the URL alone, so on a phone the system
  // Back button exited the app instead of returning to the board, and an open
  // task couldn't be linked to. Pushing state fixes both.
  useEffect(() => {
    if (bootPhase !== "ready") return;
    const view = state.view;
    const path = window.location.pathname;
    const target =
      view.kind === "board"
        ? path
        : view.kind === "detail"
          ? `${path}?task=${view.taskId}`
          : `${path}?view=${view.kind}`;
    if (path + window.location.search !== target) window.history.pushState({}, "", target);
  }, [state.view, bootPhase]);

  useEffect(() => {
    function onPopState() {
      // An open drawer owns the Back gesture: dismiss it and stay put, rather
      // than navigating out from underneath it.
      if (drawerOpenRef.current) {
        setDrawerOpen(false);
        return;
      }
      const params = new URLSearchParams(window.location.search);
      const task = Number(params.get("task"));
      const view = params.get("view");
      if (Number.isInteger(task) && task > 0) store.openDetail(task);
      else if (view === "settings") store.openSettings();
      else if (view === "routines") store.openRoutines();
      else store.openBoard();
    }
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  // Swipe in from the left edge to open the menu. The numbers are bb's: ignore
  // the first 24px so the browser's own back-swipe still works, only start
  // inside a 72px edge zone, and require a mostly-horizontal drag so a normal
  // vertical scroll never trips it.
  useEffect(() => {
    if (!isMobile) return;
    let startX = 0;
    let startY = 0;
    let tracking = false;

    function onTouchStart(e: TouchEvent) {
      if (drawerOpenRef.current || e.touches.length !== 1) return;
      const t = e.touches[0];
      if (t.clientX < 24 || t.clientX > 72) return;
      startX = t.clientX;
      startY = t.clientY;
      tracking = true;
    }
    function onTouchMove(e: TouchEvent) {
      if (!tracking) return;
      const t = e.touches[0];
      if (Math.abs(t.clientY - startY) > 40) {
        tracking = false;
        return;
      }
      if (t.clientX - startX > 48) {
        tracking = false;
        openDrawer();
      }
    }
    function onTouchEnd() {
      tracking = false;
    }

    window.addEventListener("touchstart", onTouchStart, { passive: true });
    window.addEventListener("touchmove", onTouchMove, { passive: true });
    window.addEventListener("touchend", onTouchEnd, { passive: true });
    return () => {
      window.removeEventListener("touchstart", onTouchStart);
      window.removeEventListener("touchmove", onTouchMove);
      window.removeEventListener("touchend", onTouchEnd);
    };
  }, [isMobile]);

  // Opening the drawer pushes a history entry so the phone's Back button pops
  // it; closing by any other means pops that entry back off so Back doesn't
  // need two presses afterwards.
  function openDrawer() {
    setDrawerOpen(true);
    window.history.pushState({ drawer: true }, "", window.location.href);
  }
  function closeDrawer() {
    setDrawerOpen(false);
    if (window.history.state?.drawer) window.history.back();
  }

  const projectNames = useMemo(() => state.projects.map((p) => p.name), [state.projects]);
  // A saved view is resolved by the server (the query grammar lives in
  // internal/taskfilter and is not reimplemented here), so when one is applied
  // we filter by the ids it returned. Anything typed by hand falls back to the
  // client-side grammar, which is what keeps typing instant.
  const filteredTasks = useMemo(
    () =>
      state.viewTaskIds
        ? state.tasks.filter((t) => state.viewTaskIds!.has(t.id))
        : applyFilter(state.tasks, state.filter, projectNames),
    [state.tasks, state.filter, state.viewTaskIds, projectNames],
  );
  const columns = useMemo(() => buildColumns(filteredTasks), [filteredTasks]);

  // List mode walks one flat run in display order rather than a column grid.
  const listTasks = useMemo(
    () => flattenSections(buildSections(filteredTasks, state.listOptions)),
    [filteredTasks, state.listOptions],
  );
  const inList = state.boardMode === "list" && !isMobile;

  // --- Selection helpers (shared by keyboard + board) ---
  const selectionPos = useMemo(() => {
    for (let c = 0; c < columns.length; c++) {
      const r = columns[c].tasks.findIndex((t) => t.id === state.selectedTaskId);
      if (r >= 0) return { col: c, row: r };
    }
    return null;
  }, [columns, state.selectedTaskId]);

  function moveSelection(dCol: number, dRow: number) {
    if (inList) {
      // One flat run: up/down step it, left/right mean nothing.
      if (dRow === 0 || listTasks.length === 0) return;
      const i = listTasks.findIndex((t) => t.id === state.selectedTaskId);
      const next = i < 0 ? 0 : Math.max(0, Math.min(listTasks.length - 1, i + dRow));
      store.selectTask(listTasks[next].id);
      return;
    }
    const nonEmpty = (start: number, dir: number) => {
      let c = start;
      while (c >= 0 && c < columns.length && columns[c].tasks.length === 0) c += dir;
      return c >= 0 && c < columns.length ? c : null;
    };
    if (!selectionPos) {
      const c = nonEmpty(0, 1);
      if (c !== null) store.selectTask(columns[c].tasks[0].id);
      return;
    }
    let { col, row } = selectionPos;
    if (dCol !== 0) {
      const target = nonEmpty(col + dCol, dCol);
      if (target === null) return;
      col = target;
      row = Math.min(row, columns[col].tasks.length - 1);
    } else {
      row = Math.max(0, Math.min(columns[col].tasks.length - 1, row + dRow));
    }
    store.selectTask(columns[col].tasks[row].id);
  }

  function jumpToColumn(status: string) {
    if (inList) {
      // The status keys still mean something in a flat list: jump to the first
      // task with that status wherever the current arrangement put it.
      const wanted = status === "processing" ? ["processing", "queued"] : [status];
      const task = listTasks.find((t) => wanted.includes(t.status));
      if (task) store.selectTask(task.id);
      return;
    }
    const c = columns.findIndex((col) => col.status === status);
    if (c >= 0 && columns[c].tasks.length > 0) store.selectTask(columns[c].tasks[0].id);
  }

  // --- Global keyboard ---
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const s = store.getState();

      // Escape closes overlays in priority order.
      if (e.key === "Escape") {
        if (s.paletteOpen) return void store.setPalette(false);
        if (s.dialog) return void store.setDialog(null);
        if (s.form) return void store.setForm(null);
        // These two prevent Radix's own Escape handling (see ListMenus), so
        // closing them here is the only path — and it runs before the filter,
        // so one keypress never closes a dialog and clears the filter behind it.
        if (s.arrangeOpen) return void store.setArrangeOpen(false);
        if (s.viewsOpen) return void store.setViewsOpen(false);
        if (s.filterOpen || s.filter !== "") return void store.clearFilter();
        if (s.view.kind !== "board") return void store.openBoard();
        return;
      }

      // Palette works everywhere.
      if ((e.metaKey || e.ctrlKey) && e.key === "p") {
        e.preventDefault();
        store.setPalette(true);
        return;
      }

      // The list's own overlays handle their keys too.
      if (s.paletteOpen || s.dialog || s.form || s.arrangeOpen || s.viewsOpen) return;
      if (isEditableTarget(e.target)) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      const task = s.selectedTaskId ? s.tasks.find((t) => t.id === s.selectedTaskId) : null;

      // Keys that work on board and detail alike (task-scoped).
      if (task) {
        switch (e.key) {
          case "x":
            return void store.executeTask(task.id);
          case "X":
            return void store.executeTask(task.id, true);
          case "r":
            if (task.status === "blocked") {
              e.preventDefault();
              return void store.setDialog({ kind: "retry", taskId: task.id });
            }
            break;
          case "c":
            return void store.closeTask(task.id);
          case "a":
            return void store.setDialog({
              kind: "confirm",
              title: `Archive #${task.id}?`,
              message: task.title,
              onConfirm: () => store.archiveTask(task.id),
            });
          case "d":
            return void store.setDialog({
              kind: "confirm",
              title: `Delete #${task.id}?`,
              message: `${task.title} — this cannot be undone.`,
              danger: true,
              onConfirm: () => {
                if (s.view.kind === "detail") store.openBoard();
                void store.deleteTask(task.id);
              },
            });
          case "t":
            return void store.pinTask(task.id);
          case "S":
            e.preventDefault();
            return void store.setDialog({ kind: "status", taskId: task.id });
          case "e":
            e.preventDefault();
            return void store.setForm({ kind: "edit", taskId: task.id });
          case "o":
            if (task.worktree_path) void openInEditor(task.worktree_path);
            return;
          case "b":
            if (task.branch_name) {
              void openExternal(
                task.pr_url
                  ? task.pr_url.replace(/\/pull\/\d+$/, `/tree/${task.branch_name}`)
                  : `https://github.com/search?q=${encodeURIComponent(task.branch_name)}`,
              );
            }
            return;
          case "G":
            if (task.pr_url) void openExternal(task.pr_url);
            return;
        }
      }

      switch (e.key) {
        case "?":
          return void store.setDialog({ kind: "help" });
        case "!":
          return void store.cyclePermissionMode();
        case "n":
          e.preventDefault();
          return void store.setForm({ kind: "new" });
        case "p":
        case "f":
          e.preventDefault();
          return void store.setPalette(true);
        case "R":
          return void store.refreshTasks();
      }

      if (s.view.kind === "board") {
        switch (e.key) {
          case "ArrowLeft":
            return moveSelection(-1, 0);
          case "ArrowRight":
            return moveSelection(1, 0);
          case "ArrowUp":
            return moveSelection(0, -1);
          case "ArrowDown":
            return moveSelection(0, 1);
          case "Enter":
            if (task) store.openDetail(task.id);
            return;
          case "/":
            e.preventDefault();
            store.setFilterOpen(true);
            requestAnimationFrame(() =>
              document.querySelector<HTMLInputElement>("#board-filter")?.focus(),
            );
            return;
          case "s":
            return void store.openSettings();
          case "u":
            return void store.openRoutines();
          case "[":
            return void store.toggleCollapsed("backlog");
          case "]":
            return void store.toggleCollapsed("done");
          case "B":
            return jumpToColumn("backlog");
          case "P":
            return jumpToColumn("processing");
          case "L":
            return jumpToColumn("blocked");
          case "D":
            return jumpToColumn("done");
          case "g":
            if (s.lastNotificationTaskId) store.openDetail(s.lastNotificationTaskId);
            return;
          case "v":
            return void store.toggleBoardMode();
          case "V":
            return void store.setViewsOpen(true);
          case "O":
            // Arranging a board that isn't drawn as a list changes nothing the
            // user can see, so switch to the list first (parity with the TUI).
            if (s.boardMode !== "list") store.setBoardMode("list");
            return void store.setArrangeOpen(true);
        }
      } else if (s.view.kind === "detail") {
        switch (e.key) {
          case "ArrowUp":
          case "ArrowDown": {
            // prev/next task within the same column
            if (!selectionPos) return;
            const dir = e.key === "ArrowUp" ? -1 : 1;
            const col = columns[selectionPos.col];
            const next = col.tasks[selectionPos.row + dir];
            if (next) store.openDetail(next.id);
            return;
          }
        }
      }
    }

    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (bootPhase === "setup" && envReport) {
    return (
      <SetupCheck
        report={envReport}
        onReady={() => {
          setBootPhase("starting");
          void bootApp();
        }}
        onSkip={() => {
          setupSkippedRef.current = true;
          setBootPhase("starting");
          void bootApp();
        }}
      />
    );
  }

  if (bootPhase !== "ready") {
    return (
      <div
        data-tauri-drag-region
        className="app-shell flex h-full flex-col items-center justify-center gap-4 text-muted-foreground"
      >
        <h1 className="text-lg font-semibold text-foreground">TaskYou</h1>
        {bootPhase === "error" ? (
          <>
            <div className="max-w-md select-text text-center text-destructive">{bootMessage}</div>
            <Button onClick={() => window.location.reload()}>Retry</Button>
          </>
        ) : (
          <div>{bootMessage}</div>
        )}
      </div>
    );
  }

  const permLabel = state.permissionMode === "" ? "default" : state.permissionMode;

  return (
    <div ref={shellRef} className="app-shell fixed inset-x-0 top-0 flex h-full flex-col">
      {/* Titlebar: overlay style — traffic lights sit in the left inset; the
          whole bar is a drag region. */}
      <header
        data-tauri-drag-region
        className={`flex shrink-0 items-center gap-1.5 border-b bg-surface-1 pr-2 ${
          inTauri() ? "pl-20" : "pl-3"
        } ${isMobile ? // The inset has to ADD to the header's height, not eat into it: `h-12` with
        // `pt-[env(...)]` keeps the box at 48px and simply pushes its contents
        // under the status bar, which is what put the logo level with the clock
        // on a home-screen launch. Headless Chrome resolves the inset to 0, so
        // this never showed up in testing.
        "h-[calc(3rem+env(safe-area-inset-top))] pt-[env(safe-area-inset-top)]" : "h-11"}`}
      >
        {/* Everything the phone can't fit in the header lives behind this:
            Routines, Settings, search, theme and permission mode. */}
        {isMobile && state.view.kind === "board" && (
          <Button
            variant="ghost"
            size="icon"
            className="-ml-1 size-9"
            aria-label="Menu"
            onClick={openDrawer}
          >
            <Menu className="size-5" />
          </Button>
        )}
        {/* On a phone the back button replaces the wordmark; there is no room for both. */}
        {!(isMobile && state.view.kind !== "board") && (
          <>
            <img src={logoUrl} alt="" data-tauri-drag-region className="size-5 rounded" />
            <span
              data-tauri-drag-region
              className="text-[13px] font-semibold tracking-tight text-foreground/90"
              onDoubleClick={() => store.openBoard()}
            >
              TaskYou
            </span>
          </>
        )}
        {state.view.kind !== "board" && (
          <Button
            variant="ghost"
            size="sm"
            className={isMobile ? "h-9 px-2 text-sm" : "h-7 px-2"}
            onClick={() => store.openBoard()}
          >
            <ChevronLeft data-no-drag /> Board
          </Button>
        )}
        <div data-tauri-drag-region className="flex-1" />
        {!isMobile && (
          <Badge
            variant={state.permissionMode === "dangerous" ? "destructive" : "outline"}
            className={state.permissionMode === "auto" ? "border-status-processing/50 text-status-processing" : ""}
            title="Permission mode for new executions (press !)"
            onClick={() => store.cyclePermissionMode()}
          >
            {permLabel}
          </Badge>
        )}
        <Button
          variant="ghost"
          size="sm"
          className={isMobile ? "h-9 text-sm" : "h-7"}
          title="New task (n / ⌘N)"
          onClick={() => store.setForm({ kind: "new" })}
        >
          <Plus className="size-4" /> New
        </Button>
        {/* The phone board has its own search field. */}
        {!isMobile && (
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            title="Search (⌘P)"
            onClick={() => store.setPalette(true)}
          >
            <Search className="size-4" />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          className={isMobile ? "hidden" : "size-7"}
          title={`Theme: ${state.theme} (click to cycle)`}
          onClick={() => {
            const order = ["system", "light", "dark"] as const;
            store.setTheme(order[(order.indexOf(state.theme) + 1) % order.length]);
          }}
        >
          {state.theme === "light" ? (
            <Sun className="size-4" />
          ) : state.theme === "dark" ? (
            <Moon className="size-4" />
          ) : (
            <MonitorSmartphone className="size-4" />
          )}
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className={isMobile ? "size-9" : "size-7"}
          title="Settings (⌘,)"
          onClick={() => store.openSettings()}
        >
          <Settings2 className="size-4" />
        </Button>
      </header>

      {/* Keyed remount with a fast fade-in only — exit animations make view
          switches feel sluggish, so views swap immediately. */}
      <motion.div
        key={state.view.kind === "detail" ? `detail-${state.view.taskId}` : state.view.kind}
        className="flex min-h-0 flex-1 flex-col"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ duration: 0.09, ease: "easeOut" }}
      >
          {state.view.kind === "board" &&
            (isMobile ? (
              <MobileBoard columns={columns} />
            ) : (
              <div className="flex min-h-0 flex-1 flex-col gap-2">
                {(state.filterOpen || state.filter !== "" || state.activeView !== "") && (
                  <FilterBar />
                )}
                {inList ? (
                  <div className="flex min-h-0 flex-1 flex-col gap-1 px-4 pb-4">
                    <ListToolbar />
                    <TaskList tasks={filteredTasks} options={state.listOptions} />
                  </div>
                ) : (
                  <Board columns={columns} collapsed={state.collapsed} />
                )}
              </div>
            ))}
          {state.view.kind === "detail" && <DetailView taskId={state.view.taskId} />}
          {state.view.kind === "settings" && <SettingsView />}
          {state.view.kind === "routines" && <RoutinesView />}
      </motion.div>

      {isMobile && (
        <MobileDrawer
          open={drawerOpen}
          onClose={closeDrawer}
          view={state.view.kind}
        />
      )}

      {state.paletteOpen && <Palette />}
      {state.form && <TaskForm form={state.form} />}
      <Dialogs />
      <ArrangeMenu />
      <ViewsMenu />
      <Toaster
        position={isMobile ? "top-center" : "bottom-right"}
        richColors
        closeButton
        theme={
          state.theme === "system"
            ? window.matchMedia("(prefers-color-scheme: dark)").matches
              ? "dark"
              : "light"
            : state.theme
        }
      />
    </div>
  );
}
