import { useEffect, useMemo, useRef, useState } from "react";
import { listen } from "@tauri-apps/api/event";
import { AnimatePresence, motion } from "motion/react";
import { Plus, Search, Settings2, ChevronLeft, Sun, Moon, MonitorSmartphone } from "lucide-react";
import logoUrl from "./assets/logo.png";
import { setApiBase } from "./api/client";
import { applyFilter, buildColumns } from "./lib/board";
import { store, useAppState } from "./store";
import { inTauri, openExternal, openInEditor, supervisorEnsure } from "./tauri";
import { Board } from "./components/Board";
import { DetailView } from "./components/DetailView";
import { SettingsView } from "./components/SettingsView";
import { Palette } from "./components/Palette";
import { TaskForm } from "./components/TaskForm";
import { Dialogs } from "./components/Dialogs";
import { FilterBar } from "./components/FilterBar";
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
  const [bootPhase, setBootPhase] = useState<"starting" | "ready" | "error">("starting");
  const [bootMessage, setBootMessage] = useState("Starting TaskYou…");
  const bootedRef = useRef(false);

  // --- Boot: supervise sidecars, then load data ---
  useEffect(() => {
    if (bootedRef.current) return;
    bootedRef.current = true;
    (async () => {
      try {
        if (inTauri()) {
          setBootMessage("Checking TaskYou server…");
          const status = await supervisorEnsure();
          setApiBase(`http://127.0.0.1:${status.port}`);
        }
        setBootMessage("Loading board…");
        await store.boot();
        setBootPhase("ready");
      } catch (e) {
        setBootMessage(e instanceof Error ? e.message : String(e));
        setBootPhase("error");
      }
    })();
  }, []);

  // --- Native menu events ---
  useEffect(() => {
    if (!inTauri()) return;
    const unlisten = listen<string>("menu", ({ payload }) => {
      switch (payload) {
        case "new-task":
          return void store.setForm({ kind: "new" });
        case "settings":
          return void store.openSettings();
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

  const projectNames = useMemo(() => state.projects.map((p) => p.name), [state.projects]);
  const filteredTasks = useMemo(
    () => applyFilter(state.tasks, state.filter, projectNames),
    [state.tasks, state.filter, projectNames],
  );
  const columns = useMemo(() => buildColumns(filteredTasks), [filteredTasks]);

  // --- Selection helpers (shared by keyboard + board) ---
  const selectionPos = useMemo(() => {
    for (let c = 0; c < columns.length; c++) {
      const r = columns[c].tasks.findIndex((t) => t.id === state.selectedTaskId);
      if (r >= 0) return { col: c, row: r };
    }
    return null;
  }, [columns, state.selectedTaskId]);

  function moveSelection(dCol: number, dRow: number) {
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
        if (s.filterOpen || s.filter !== "") {
          store.setFilterOpen(false);
          store.setFilter("");
          return;
        }
        if (s.view.kind !== "board") return void store.openBoard();
        return;
      }

      // Palette works everywhere.
      if ((e.metaKey || e.ctrlKey) && e.key === "p") {
        e.preventDefault();
        store.setPalette(true);
        return;
      }

      if (s.paletteOpen || s.dialog || s.form) return; // modal components handle their own keys
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
    <div className="app-shell flex h-full flex-col">
      {/* Titlebar: overlay style — traffic lights sit in the left inset; the
          whole bar is a drag region. */}
      <header
        data-tauri-drag-region
        className={`flex h-11 shrink-0 items-center gap-1.5 border-b bg-surface-1 pr-2 ${
          inTauri() ? "pl-20" : "pl-3"
        }`}
      >
        <img src={logoUrl} alt="" data-tauri-drag-region className="size-5 rounded" />
        <span
          data-tauri-drag-region
          className="text-[13px] font-semibold tracking-tight text-foreground/90"
          onDoubleClick={() => store.openBoard()}
        >
          TaskYou
        </span>
        {state.view.kind !== "board" && (
          <Button variant="ghost" size="sm" className="h-7 px-2" onClick={() => store.openBoard()}>
            <ChevronLeft data-no-drag /> Board
          </Button>
        )}
        <div data-tauri-drag-region className="flex-1" />
        <Badge
          variant={state.permissionMode === "dangerous" ? "destructive" : "outline"}
          className={state.permissionMode === "auto" ? "border-status-processing/50 text-status-processing" : ""}
          title="Permission mode for new executions (press !)"
          onClick={() => store.cyclePermissionMode()}
        >
          {permLabel}
        </Badge>
        <Button
          variant="ghost"
          size="sm"
          className="h-7"
          title="New task (n / ⌘N)"
          onClick={() => store.setForm({ kind: "new" })}
        >
          <Plus className="size-4" /> New
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          title="Search (⌘P)"
          onClick={() => store.setPalette(true)}
        >
          <Search className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
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
          className="size-7"
          title="Settings (⌘,)"
          onClick={() => store.openSettings()}
        >
          <Settings2 className="size-4" />
        </Button>
      </header>

      <AnimatePresence mode="wait" initial={false}>
        <motion.div
          key={state.view.kind === "detail" ? `detail-${state.view.taskId}` : state.view.kind}
          className="flex min-h-0 flex-1 flex-col"
          initial={{ opacity: 0, y: 4 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -4 }}
          transition={{ duration: 0.14, ease: "easeOut" }}
        >
          {state.view.kind === "board" && (
            <div className="flex min-h-0 flex-1 flex-col">
              {(state.filterOpen || state.filter !== "") && <FilterBar />}
              <Board columns={columns} collapsed={state.collapsed} />
            </div>
          )}
          {state.view.kind === "detail" && <DetailView taskId={state.view.taskId} />}
          {state.view.kind === "settings" && <SettingsView />}
        </motion.div>
      </AnimatePresence>

      {state.paletteOpen && <Palette />}
      {state.form && <TaskForm form={state.form} />}
      <Dialogs />
      <Toaster
        position="bottom-right"
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
