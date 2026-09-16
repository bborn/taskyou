import { useMemo, useState } from "react";
import { AnimatePresence } from "motion/react";
import { Check, ChevronDown, ChevronRight, ListFilter, X } from "lucide-react";
import type { Column } from "../lib/board";
import { parseFilter, referenceTime } from "../lib/board";
import { store, useAppSelector } from "../store";
import { CardSlot } from "./Board";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const FILTERS = [
  { key: "blocked", label: "Needs you", token: "blocked" },
  { key: "processing", label: "Running", token: "running" },
  { key: "backlog", label: "Backlog", token: "backlog" },
  { key: "done", label: "Done", token: "done" },
] as const;

type FilterKey = (typeof FILTERS)[number]["key"];

const ACCENT: Record<FilterKey, string> = {
  blocked: "text-status-blocked",
  processing: "text-status-processing",
  backlog: "text-status-backlog",
  done: "text-foreground",
};

const EMPTY: Record<FilterKey, string> = {
  blocked: "Nothing waiting on you.",
  processing: "Nothing running right now.",
  backlog: "Backlog is empty.",
  done: "Nothing finished yet.",
};

/** Cards rendered before a "show more" button; Done alone can hold hundreds. */
const RENDER_CAP = 50;

const STATUS_TOKEN = /\bis:[a-z-]+/gi;
const PROJECT_TOKEN = /\[[^\]]*\]?/g;

/** Rewrite one token of the filter string, leaving the others alone. The whole
 * control edits a single string, so project/status/text can't disagree. */
function withToken(filter: string, token: RegExp, next: string | null): string {
  const rest = filter.replace(token, "").replace(/\s+/g, " ").trim();
  if (!next) return rest;
  return rest ? `${next} ${rest}` : next;
}

function textOf(filter: string): string {
  return filter.replace(STATUS_TOKEN, "").replace(PROJECT_TOKEN, "").replace(/\s+/g, " ").trim();
}

/**
 * Phone board: one scrolling list, and ONE filter control.
 *
 * Project chips, a status pill and a search button could not share 390px —
 * every arrangement sliced a chip. They are all the same filter string
 * underneath (`is:running [offerlab] text`), so they are now one field that
 * opens a sheet.
 *
 * No filter means no filter: with no `is:` token the list is every task,
 * newest first, not a status picked on your behalf.
 */
export function MobileBoard({ columns }: { columns: Column[] }) {
  const projects = useAppSelector((s) => s.projects);
  const tasks = useAppSelector((s) => s.tasks);
  const latestLogs = useAppSelector((s) => s.latestLogs);
  const filter = useAppSelector((s) => s.filter);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  // Fold state is deliberately not persisted — deck does the same; it is a
  // glance-level toggle, not a preference.
  const [pinnedFolded, setPinnedFolded] = useState(false);

  const parsed = parseFilter(filter);
  const activeStatus: FilterKey | null = (parsed.status as FilterKey | undefined) ?? null;
  const activeProject = parsed.project ?? null;
  const searchText = textOf(filter);

  // Unfiltered: every column flattened and ordered by the same rule the columns
  // use internally, so a mixed list still reads newest-first with pins on top.
  const listTasks = useMemo(() => {
    if (activeStatus) return columns.find((c) => c.status === activeStatus)?.tasks ?? [];
    return columns
      .flatMap((c) => c.tasks)
      .sort((a, b) => {
        if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
        return referenceTime(b) - referenceTime(a);
      });
  }, [columns, activeStatus]);

  // Pins lead in their own group, as bb's deck does: scattering them through
  // the list is what makes pinning pointless. The render cap applies to the
  // remainder only — a pinned task must never fall outside the cap and vanish
  // from the very group that exists to keep it visible.
  const pinnedTasks = useMemo(() => listTasks.filter((t) => t.pinned), [listTasks]);
  const restTasks = useMemo(() => listTasks.filter((t) => !t.pinned), [listTasks]);
  const visible = showAll ? restTasks : restTasks.slice(0, RENDER_CAP);

  // Counts ignore the dimension they describe: status counts span every status,
  // project counts sit within the chosen one (or all of them when none is set).
  const { statusCounts, projectChips, grandTotal } = useMemo(() => {
    const text = searchText.toLowerCase();
    const matchesText = (t: (typeof tasks)[number]) =>
      !text || t.title.toLowerCase().includes(text) || t.body.toLowerCase().includes(text);

    const byStatus = new Map<string, number>();
    const byProject = new Map<string, number>();
    let total = 0;
    for (const t of tasks) {
      if (t.status === "archived" || !matchesText(t)) continue;
      const status = t.status === "queued" ? "backlog" : t.status;
      if (!activeProject || t.project === activeProject) {
        byStatus.set(status, (byStatus.get(status) ?? 0) + 1);
        total++;
      }
      if (!activeStatus || status === activeStatus) {
        byProject.set(t.project, (byProject.get(t.project) ?? 0) + 1);
      }
    }

    const chips = projects
      .map((project) => ({ project, count: byProject.get(project.name) ?? 0 }))
      .filter((c) => c.count > 0 || c.project.name === activeProject)
      .sort((a, b) => b.count - a.count || a.project.name.localeCompare(b.project.name));

    return { statusCounts: byStatus, projectChips: chips, grandTotal: total };
  }, [tasks, projects, activeStatus, activeProject, searchText]);

  // "All projects" sits above rows counted within the chosen status, so it has
  // to be that status's total — or everything when no status is chosen.
  const totalForProjects = activeStatus ? (statusCounts.get(activeStatus) ?? 0) : grandTotal;

  // Picking a status or a project is a decision; close the sheet so you land
  // back on the list. Typing is not, so the text field leaves it open.
  function setStatus(key: FilterKey | null) {
    const token = key ? `is:${FILTERS.find((f) => f.key === key)!.token}` : null;
    store.setFilter(withToken(filter, STATUS_TOKEN, token));
    setShowAll(false);
    setSheetOpen(false);
  }
  function setProject(name: string | null) {
    store.setFilter(withToken(filter, PROJECT_TOKEN, name ? `[${name}]` : null));
    setShowAll(false);
    setSheetOpen(false);
  }
  function setText(next: string) {
    const status = filter.match(STATUS_TOKEN)?.[0];
    const project = filter.match(PROJECT_TOKEN)?.[0];
    store.setFilter([status, project, next.trim()].filter(Boolean).join(" "));
    setShowAll(false);
  }
  function clearAll() {
    store.setFilter("");
    setShowAll(false);
  }

  const statusLabel = activeStatus ? FILTERS.find((f) => f.key === activeStatus)!.label : "All tasks";
  const filtered = activeStatus !== null || activeProject !== null || searchText !== "";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* One control: status, project and text in a single field. */}
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <button
          onClick={() => setSheetOpen(true)}
          className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-lg border bg-surface-1 px-3 text-left text-[13px]"
        >
          <ListFilter className="size-4 shrink-0 text-muted-foreground" />
          <span
            className={cn(
              "shrink-0 font-medium",
              activeStatus ? ACCENT[activeStatus] : "text-foreground",
            )}
          >
            {statusLabel}
          </span>
          <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
            {listTasks.length}
          </span>
          {activeProject && (
            <span className="min-w-0 truncate text-muted-foreground">· {activeProject}</span>
          )}
          {searchText && <span className="min-w-0 truncate text-muted-foreground">· {searchText}</span>}
        </button>
        {filtered && (
          <button
            onClick={clearAll}
            aria-label="Clear filter"
            className="flex size-10 shrink-0 items-center justify-center rounded-lg text-muted-foreground active:bg-surface-2"
          >
            <X className="size-4" />
          </button>
        )}
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain px-3 pt-2 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        {listTasks.length === 0 ? (
          <div className="px-4 py-12 text-center text-sm text-muted-foreground">
            {filtered
              ? activeStatus && !activeProject && !searchText
                ? EMPTY[activeStatus]
                : "No tasks match that filter."
              : "No tasks yet."}
          </div>
        ) : (
          <>
            {pinnedTasks.length > 0 && (
              <>
                <button
                  onClick={() => setPinnedFolded(!pinnedFolded)}
                  className="-mx-1 flex items-center gap-1.5 px-1 py-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase active:text-foreground"
                >
                  {pinnedFolded ? (
                    <ChevronRight className="size-3" />
                  ) : (
                    <ChevronDown className="size-3" />
                  )}
                  Pinned
                  <span className="tabular-nums opacity-70">{pinnedTasks.length}</span>
                </button>
                {!pinnedFolded && (
                  <AnimatePresence initial={false}>
                    {pinnedTasks.map((task) => (
                      <CardSlot
                        key={task.id}
                        task={task}
                        selected={false}
                        tapToOpen
                        projectColor={
                          projects.find((p) => p.name === task.project)?.color ||
                          "var(--muted-foreground)"
                        }
                        latest={latestLogs[String(task.id)]}
                      />
                    ))}
                  </AnimatePresence>
                )}
                {restTasks.length > 0 && <div className="my-1 border-t" />}
              </>
            )}
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
          </>
        )}
        {restTasks.length > visible.length && (
          <button
            className="rounded-lg py-3 text-center text-[13px] text-muted-foreground active:bg-surface-2"
            onClick={() => setShowAll(true)}
          >
            {restTasks.length - visible.length} more…
          </button>
        )}
      </div>

      <Dialog open={sheetOpen} onOpenChange={setSheetOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Filter</DialogTitle>
          </DialogHeader>

          {/* 16px text: iOS Safari zooms the page when focusing anything smaller. */}
          <Input
            value={searchText}
            className="h-11 text-base md:text-base"
            placeholder="Search title, body, or #123"
            onChange={(e) => setText(e.target.value)}
          />

          <Section title="Status" />
          <Row
            label="All statuses"
            count={grandTotal}
            selected={activeStatus === null}
            onClick={() => setStatus(null)}
          />
          {FILTERS.map((f) => (
            <Row
              key={f.key}
              label={f.label}
              count={statusCounts.get(f.key) ?? 0}
              selected={activeStatus === f.key}
              onClick={() => setStatus(f.key)}
            />
          ))}

          <Section title="Project" />
          <Row
            label="All projects"
            count={totalForProjects}
            selected={activeProject === null}
            onClick={() => setProject(null)}
          />
          {projectChips.map(({ project, count }) => (
            <Row
              key={project.id}
              label={project.name}
              count={count}
              color={project.color}
              selected={activeProject === project.name}
              onClick={() => setProject(project.name)}
            />
          ))}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Section({ title }: { title: string }) {
  return (
    <div className="mt-1 text-[11px] font-semibold tracking-wider text-muted-foreground uppercase">
      {title}
    </div>
  );
}

function Row({
  label,
  count,
  color,
  selected,
  onClick,
}: {
  label: string;
  count: number;
  color?: string;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "-mx-1 flex h-11 items-center gap-2.5 rounded-lg px-3 text-left text-[15px] active:bg-surface-2",
        selected ? "font-medium text-foreground" : "text-muted-foreground",
      )}
    >
      {color !== undefined && (
        <span
          className="size-2 shrink-0 rounded-full"
          style={{ background: color || "var(--muted-foreground)" }}
        />
      )}
      <span className="min-w-0 truncate">{label}</span>
      <span className="ml-auto shrink-0 text-[13px] tabular-nums opacity-70">{count}</span>
      {selected && <Check className="size-4 shrink-0" />}
    </button>
  );
}
