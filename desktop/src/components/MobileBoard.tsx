import { useEffect, useMemo, useState, useRef, type ReactNode } from "react";
import { Check, ChevronDown, X } from "lucide-react";
import type { Task, TaskCounts } from "../api/types";
import { api } from "../api/client";
import { parseFilter } from "../lib/board";
import { normalizeListOptions, DEFAULT_LIST_OPTIONS, GROUP_BY_OPTIONS, SORT_OPTIONS, type ListGroupBy, type ListSort } from "../lib/list";
import { store, useAppSelector } from "../store";
import { usePersistedToggle } from "../hooks/use-persisted-toggle";
import { TaskList } from "./TaskList";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// The four pills must partition the board — every task answers to exactly one,
// or tasks fall through the gaps. "In progress" is queued AND processing, the
// same pairing the kanban column and the list section make: a queued task used
// to belong to neither "Running" (processing only) nor "Backlog".
const FILTERS = [
  { key: "blocked", label: "Needs you", token: "blocked" },
  { key: "processing", label: "In progress", token: "in-progress" },
  { key: "backlog", label: "Backlog", token: "backlog" },
  { key: "done", label: "Done", token: "done" },
] as const;

/** Which pill a task belongs to. */
function pillFor(status: string): FilterKey | null {
  switch (status) {
    case "blocked":
      return "blocked";
    case "queued":
    case "processing":
      return "processing";
    case "backlog":
      return "backlog";
    case "done":
      return "done";
    default:
      return null; // archived
  }
}

type FilterKey = (typeof FILTERS)[number]["key"];

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

export function MobileBoard({ tasks, filterOpen, onFilterClose }: {
  tasks: Task[];
  filterOpen: boolean;
  onFilterClose: () => void;
}) {
  const options = useAppSelector((s) => s.listOptions);
  return <div className="flex min-h-0 flex-1 flex-col">
    <TaskList tasks={tasks} options={options} variant="card" />
    {filterOpen && <MobileFilters onClose={onFilterClose} />}
  </div>;
}

const DRAFT_KEY = "ty:mobile-filter-draft";
function readDraft(baseFilter: string) {
  try {
    const draft = JSON.parse(localStorage.getItem(DRAFT_KEY) || "null");
    if (draft?.baseFilter === baseFilter && typeof draft.filter === "string" &&
        typeof draft.search === "string" && typeof draft.view === "string" && draft.options) {
      return { filter: draft.filter as string, search: draft.search as string,
        view: draft.view as string, options: normalizeListOptions(draft.options) };
    }
  } catch { /* Invalid or unavailable storage falls back to the applied settings. */ }
  return null;
}

// Keep unfinished selections across closing/reloading; Apply still owns the list.
function MobileFilters({ onClose }: { onClose: () => void }) {
  const searchRef = useRef<HTMLInputElement>(null);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const projects = useAppSelector((s) => s.projects);
  const tasks = useAppSelector((s) => s.tasks);
  const appliedFilter = useAppSelector((s) => s.filter);
  const [draft] = useState(() => readDraft(appliedFilter));
  const [filter, setFilter] = useState(draft?.filter ?? appliedFilter);
  const appliedOptions = useAppSelector((s) => s.listOptions);
  const [listOptions, setListOptions] = useState(draft?.options ?? appliedOptions);
  const savedViews = useAppSelector((s) => s.savedViews);
  const appliedView = useAppSelector((s) => s.activeView);
  const [activeView, setActiveView] = useState(draft?.view ?? appliedView);
  // Keep keystrokes verbatim: the serialized filter trims whitespace, which
  // otherwise eats the space before the user can type the next word.
  const [searchInput, setSearchInput] = useState(() => draft?.search ?? textOf(appliedFilter));

  useEffect(() => {
    try {
      localStorage.setItem(DRAFT_KEY, JSON.stringify({baseFilter: appliedFilter,
        filter, search: searchInput, view: activeView, options: listOptions}));
    } catch { /* Keep the controls usable when storage is disabled. */ }
  }, [appliedFilter, filter, searchInput, activeView, listOptions]);

  const parsed = parseFilter(filter);
  const activeStatus: FilterKey | null = (parsed.status as FilterKey | undefined) ?? null;
  const activeProject = parsed.project ?? null;

  // Counts ignore the dimension they describe: the status counts drop the status
  // token from the query, the project counts drop the project token. They are
  // resolved by the server for the same reason the list is — a count produced by
  // a different parser than the filter is a number that disagrees with the rows
  // underneath it.
  const statusQuery = useMemo(() => withToken(filter, STATUS_TOKEN, null), [filter]);
  const projectQuery = useMemo(() => withToken(filter, PROJECT_TOKEN, null), [filter]);
  // The server counts across every task: the loaded list is only the newest
  // 1000, and counting it read "973 tasks, 857 done" on a board with 2,100 done.
  const statusCountsRaw = useTaskCounts(statusQuery, tasks);
  const projectCountsRaw = useTaskCounts(projectQuery, tasks);

  const { statusCounts, projectChips, grandTotal, projectTotal } = useMemo(() => {
    const byStatus = new Map<string, number>();
    for (const [status, n] of Object.entries(statusCountsRaw?.status ?? {})) {
      const pill = pillFor(status);
      if (pill) byStatus.set(pill, (byStatus.get(pill) ?? 0) + n);
    }
    const byProject = projectCountsRaw?.project ?? {};

    const chips = projects
      .map((project) => ({ project, count: byProject[project.name] ?? 0 }))
      .filter((c) => c.count > 0 || c.project.name === activeProject)
      .sort((a, b) => b.count - a.count || a.project.name.localeCompare(b.project.name));

    return {
      statusCounts: statusCountsRaw ? byStatus : null,
      projectChips: chips,
      grandTotal: statusCountsRaw?.total,
      projectTotal: projectCountsRaw?.total,
    };
  }, [statusCountsRaw, projectCountsRaw, projects, activeProject]);

  // "All projects" sits above rows counted within the chosen status, so it has
  // to be that status's total — or everything when no status is chosen.
  const totalForProjects = projectTotal;

  // Selection edits the draft; only Apply changes the board.
  function setStatus(key: FilterKey | null) {
    const token = key ? `is:${FILTERS.find((f) => f.key === key)!.token}` : null;
    setFilter(withToken(filter, STATUS_TOKEN, token));
    setActiveView("");
  }
  function setProject(name: string | null) {
    setFilter(withToken(filter, PROJECT_TOKEN, name ? `[${name}]` : null));
    setActiveView("");
  }
  function setText(next: string) {
    setSearchInput(next);
    const status = filter.match(STATUS_TOKEN)?.[0];
    const project = filter.match(PROJECT_TOKEN)?.[0];
    setFilter([status, project, next.trim()].filter(Boolean).join(" "));
    setActiveView("");
  }
  function reset() {
    store.clearFilter();
    store.setListOptions(DEFAULT_LIST_OPTIONS);
    setFilter("");
    setSearchInput("");
    setActiveView("");
    setListOptions(DEFAULT_LIST_OPTIONS);
  }
  function apply() {
    try { localStorage.removeItem(DRAFT_KEY); } catch { /* Storage may be disabled. */ }
    if (activeView) void store.applyView(activeView);
    else store.setFilter(filter);
    store.setListOptions(listOptions);
    onClose();
  }

  return (
      <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
        <DialogContent className="max-w-md flex flex-col gap-0 overflow-hidden" onOpenAutoFocus={(event) => { event.preventDefault(); titleRef.current?.focus(); }}>
          <DialogHeader className="shrink-0 pb-4">
            <DialogTitle ref={titleRef} tabIndex={-1} className="outline-none">Filter</DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain overflow-x-hidden space-y-3 pb-4">
          {/* 16px text: iOS Safari zooms the page when focusing anything smaller. */}
          <div className="relative">
            <Input
              ref={searchRef}
              value={searchInput}
              className="h-11 pr-12 text-base md:text-base"
              placeholder="Search title, body, or #123"
              onChange={(e) => setText(e.target.value)}
            />
            {searchInput.length > 0 && (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="absolute top-0 right-0 size-11 text-muted-foreground"
                aria-label="Clear search"
                onPointerDown={(event) => event.preventDefault()}
                onClick={() => { setText(""); searchRef.current?.focus(); }}
              >
                <X className="size-4" aria-hidden="true" />
              </Button>
            )}
          </div>

          {savedViews.length > 0 && (
            <Section title="Views">
              <Row label="All tasks" selected={!activeView && !filter.trim()} onClick={() => {
                setActiveView(""); setFilter(""); setSearchInput("");
              }} />
              {savedViews.map((view) => (
                <Row
                  key={view.id}
                  label={view.name}
                  selected={activeView === view.name}
                  onClick={() => {
                    const clear = activeView === view.name;
                    setActiveView(clear ? "" : view.name);
                    setFilter(clear ? "" : view.query);
                    setSearchInput(clear ? "" : textOf(view.query));
                  }}
                />
              ))}
            </Section>
          )}

          <Section title="Status" defaultOpen>
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
              count={statusCounts ? statusCounts.get(f.key) ?? 0 : undefined}
              selected={activeStatus === f.key}
              onClick={() => setStatus(f.key)}
            />
          ))}

          </Section>
          <Section title="Projects">
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

          </Section>
          {/* Arrangement lives in this sheet rather than a second control, for
              the same reason everything else does: 390px only fits one. */}
          <Section title="Group by">
          <Chips
            options={GROUP_BY_OPTIONS}
            value={listOptions.groupBy}
            onChange={(groupBy: ListGroupBy) => setListOptions({ ...listOptions, groupBy })}
          />
          </Section>
          <Section title="Sort">
          <Chips
            options={SORT_OPTIONS}
            value={listOptions.sort}
            onChange={(sort: ListSort) => setListOptions({ ...listOptions, sort })}
          />
          </Section>
          </div>
          <div className="flex shrink-0 gap-3 border-t pt-3">
            <Button variant="outline" className="h-11 flex-1" onClick={reset}>Reset</Button>
            <Button className="h-11 flex-1" onClick={apply}>Apply</Button>
          </div>
        </DialogContent>
      </Dialog>
  );
}

function Section({ title, children, defaultOpen = false }: { title: string; children: ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = usePersistedToggle(`ty:filter-section:${title}`, defaultOpen);
  return <details open={open} onToggle={(event) => setOpen(event.currentTarget.open)} className="group/filter-section border-t">
    <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between text-xs font-semibold text-muted-foreground [&::-webkit-details-marker]:hidden">
      {title}<ChevronDown className="size-4 transition-transform group-open/filter-section:rotate-180" />
    </summary>
    <div className="flex flex-col gap-1 pb-2">{children}</div>
  </details>;
}

function Chips<T extends string>({
  options,
  value,
  onChange,
}: {
  options: readonly T[];
  value: T;
  onChange: (next: T) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((opt) => (
        <button
          key={opt}
          onClick={() => onChange(opt)}
          className={cn(
            "h-11 rounded-lg px-3 text-[13px] capitalize",
            opt === value
              ? "bg-primary font-medium text-primary-foreground"
              : "bg-surface-2 text-muted-foreground",
          )}
        >
          {opt}
        </button>
      ))}
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
  count?: number;
  color?: string;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      aria-pressed={selected}
      className={cn(
        "flex h-11 shrink-0 items-center gap-2.5 rounded-lg px-3 text-left text-[15px] active:bg-surface-2",
        selected ? "font-medium text-foreground" : "text-muted-foreground",
      )}
    >
      {color !== undefined && (
        <span
          className="size-2 shrink-0 rounded-full"
          style={{ background: color || "var(--muted-foreground)" }}
        />
      )}
      <span className={cn("min-w-0 flex-1 truncate", count === undefined && "flex-1")}>{label}</span>
      {count !== undefined && (
        <span className="ml-auto shrink-0 text-[13px] tabular-nums opacity-70">{count}</span>
      )}
      {selected && <Check className="size-4 shrink-0" />}
    </button>
  );
}

/** Tasks matching a query, resolved by the server so the sheet's counts use the
 * same grammar as the filter itself. Only runs while the sheet is open; falls
 * back to the unfiltered set so a count is never blank. */
function useTaskCounts(query: string, board: Task[]): TaskCounts | null {
  const [counts, setCounts] = useState<TaskCounts | null>(null);

  // Refetched when the query changes and whenever the board does (`board` is a
  // new array on every refresh), so the counts follow tasks as they move.
  useEffect(() => {
    let live = true;
    void api
      .taskCounts(query.trim())
      .then((c) => {
        if (live) setCounts(c);
      })
      .catch(() => {
        if (live) setCounts(null);
      });
    return () => {
      live = false;
    };
  }, [query, board]);

  return counts;
}
