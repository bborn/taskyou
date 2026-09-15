import { useEffect, useMemo, useState } from "react";
import { AnimatePresence } from "motion/react";
import { Search, X } from "lucide-react";
import type { Column } from "../lib/board";
import { parseFilter } from "../lib/board";
import { store, useAppSelector } from "../store";
import { CardSlot } from "./Board";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";

/** "Needs you" comes first: the point of the phone is unblocking work you
 * can't see, not browsing the backlog. */
const FILTERS = [
  { key: "blocked", label: "Needs you" },
  { key: "processing", label: "Running" },
  { key: "backlog", label: "Backlog" },
  { key: "done", label: "Done" },
] as const;

type FilterKey = (typeof FILTERS)[number]["key"];

const ACCENT: Record<FilterKey, string> = {
  blocked: "border-status-blocked/60 bg-status-blocked/15 text-status-blocked",
  processing: "border-status-processing/60 bg-status-processing/15 text-status-processing",
  backlog: "border-status-backlog/60 bg-status-backlog/15 text-status-backlog",
  done: "border-border bg-surface-3 text-foreground",
};

const EMPTY: Record<FilterKey, string> = {
  blocked: "Nothing waiting on you.",
  processing: "Nothing running right now.",
  backlog: "Backlog is empty.",
  done: "Nothing finished yet.",
};

/** Cards rendered before a "show more" button; Done alone can hold hundreds. */
const RENDER_CAP = 50;

/** Rewrite only the `[project]` token, leaving any typed text in place — the
 * chips and the search box drive the same filter string. */
function withProject(filter: string, project: string | null): string {
  const rest = filter.replace(/\[[^\]]*\]?/, "").trim();
  if (!project) return rest;
  return rest ? `[${project}] ${rest}` : `[${project}]`;
}

/** Phone board: one scrolling list at a time instead of four side-by-side
 * columns. Cards are the desktop component, so the layouts can't drift. */
export function MobileBoard({ columns }: { columns: Column[] }) {
  const projects = useAppSelector((s) => s.projects);
  const tasks = useAppSelector((s) => s.tasks);
  const latestLogs = useAppSelector((s) => s.latestLogs);
  const filter = useAppSelector((s) => s.filter);
  const [active, setActive] = useState<FilterKey>("blocked");
  const [searchOpen, setSearchOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [landed, setLanded] = useState(false);

  const count = (key: FilterKey) => columns.find((c) => c.status === key)?.tasks.length ?? 0;
  const columnTasks = columns.find((c) => c.status === active)?.tasks ?? [];
  const visible = showAll ? columnTasks : columnTasks.slice(0, RENDER_CAP);

  // Which project chip is lit. Chips write an exact name, so an exact match is
  // enough here — the fuzzy path in applyFilter is for hand-typed filters.
  const activeProject = parseFilter(filter).project ?? null;

  // Counts are per-project *within the current status tab*, and come from the
  // unfiltered task list — reading them off `columns` would count only what
  // the active chip already let through.
  //
  // Busiest first, and empty projects are dropped: in API order the three
  // projects holding every blocked task sat off the right edge behind three
  // chips reading 0. The active chip is always kept so it can't vanish under
  // the tap that selected it.
  const { chips, allCount } = useMemo(() => {
    const counts = new Map<string, number>();
    let total = 0;
    for (const t of tasks) {
      const status = t.status === "queued" ? "backlog" : t.status;
      if (status !== active) continue;
      total++;
      counts.set(t.project, (counts.get(t.project) ?? 0) + 1);
    }
    const list = projects
      .map((project) => ({ project, count: counts.get(project.name) ?? 0 }))
      .filter((c) => c.count > 0 || c.project.name === activeProject)
      .sort((a, b) => b.count - a.count || a.project.name.localeCompare(b.project.name));
    return { chips: list, allCount: total };
  }, [projects, tasks, active, activeProject]);

  // First load: an empty "Needs you" is good news, but land on the work that
  // is actually moving instead of an empty list.
  useEffect(() => {
    if (landed || columns.every((c) => c.tasks.length === 0)) return;
    setLanded(true);
    if (count("blocked") === 0) {
      setActive(count("processing") > 0 ? "processing" : "backlog");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [landed, columns]);

  function choose(key: FilterKey) {
    setActive(key);
    setShowAll(false);
  }

  function chooseProject(name: string | null) {
    store.setFilter(withProject(filter, name));
    setShowAll(false);
  }

  function closeSearch() {
    setSearchOpen(false);
    // Keep the project chip; only drop the typed text.
    store.setFilter(activeProject ? `[${activeProject}]` : "");
  }

  const searchText = filter.replace(/\[[^\]]*\]?/, "").trim();
  const searchVisible = searchOpen || searchText !== "";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Project first: with a hundred-odd tasks spread over four repos, which
          project you're looking at is a bigger cut than which status. */}
      {chips.length > 1 && (
        <div className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-3 py-2 [scrollbar-width:none]">
          <button
            onClick={() => chooseProject(null)}
            className={cn(
              "flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-[13px] font-medium transition-colors",
              activeProject === null
                ? "border-border bg-surface-3 text-foreground"
                : "border-transparent bg-surface-2 text-muted-foreground",
            )}
          >
            All
            {/* Across every project — `columns` is already project-filtered, so
                reading the count from there made "All" report 0 as soon as a
                project chip was lit, hiding the way back to everything. */}
            <span className="text-[11px] tabular-nums opacity-70">{allCount}</span>
          </button>
          {chips.map(({ project: p, count: n }) => (
            <button
              key={p.id}
              onClick={() => chooseProject(activeProject === p.name ? null : p.name)}
              className={cn(
                "flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-[13px] font-medium transition-colors",
                activeProject === p.name
                  ? "border-border bg-surface-3 text-foreground"
                  : "border-transparent bg-surface-2 text-muted-foreground",
              )}
            >
              <span
                className="size-2 shrink-0 rounded-full"
                style={{ background: p.color || "var(--muted-foreground)" }}
              />
              {p.name}
              <span className="text-[11px] tabular-nums opacity-70">{n}</span>
            </button>
          ))}
        </div>
      )}

      <div className="flex shrink-0 items-center gap-1.5 border-b px-3 py-2">
        {/* Chips scroll on their own so the search button stays pinned. */}
        <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto [scrollbar-width:none]">
          {FILTERS.map((f) => (
            <button
              key={f.key}
              onClick={() => choose(f.key)}
              className={cn(
                "flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3.5 text-[13px] font-medium transition-colors",
                active === f.key ? ACCENT[f.key] : "border-transparent bg-surface-2 text-muted-foreground",
              )}
            >
              {f.label}
              <span className="text-[11px] tabular-nums opacity-70">{count(f.key)}</span>
            </button>
          ))}
        </div>
        <button
          onClick={() => (searchVisible ? closeSearch() : setSearchOpen(true))}
          aria-label={searchVisible ? "Close search" : "Search tasks"}
          className={cn(
            "flex size-9 shrink-0 items-center justify-center rounded-full",
            searchVisible ? "bg-surface-3 text-foreground" : "text-muted-foreground",
          )}
        >
          {searchVisible ? <X className="size-4" /> : <Search className="size-4" />}
        </button>
      </div>

      {searchVisible && (
        <div className="shrink-0 px-3 pt-2">
          {/* 16px text: iOS Safari zooms the page when focusing anything smaller. */}
          <Input
            autoFocus
            value={searchText}
            className="h-10 text-base md:text-base"
            placeholder="Search title, body, or #123"
            onChange={(e) => store.setFilter(withProject(e.target.value, activeProject))}
          />
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain px-3 pt-2 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        {columnTasks.length === 0 ? (
          <div className="px-4 py-12 text-center text-sm text-muted-foreground">
            {filter ? "No tasks match that filter." : EMPTY[active]}
          </div>
        ) : (
          <AnimatePresence initial={false}>
            {visible.map((task) => (
              <CardSlot
                key={task.id}
                task={task}
                selected={false}
                tapToOpen
                projectColor={projects.find((p) => p.name === task.project)?.color || "var(--muted-foreground)"}
                latest={latestLogs[String(task.id)]}
              />
            ))}
          </AnimatePresence>
        )}
        {columnTasks.length > visible.length && (
          <button
            className="rounded-lg py-3 text-center text-[13px] text-muted-foreground active:bg-surface-2"
            onClick={() => setShowAll(true)}
          >
            {columnTasks.length - visible.length} more…
          </button>
        )}
      </div>
    </div>
  );
}
