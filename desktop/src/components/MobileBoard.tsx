import { useMemo, useState } from "react";
import { Check, ListFilter, X } from "lucide-react";
import type { Task } from "../api/types";
import { parseFilter } from "../lib/board";
import { GROUP_BY_OPTIONS, SORT_OPTIONS, type ListGroupBy, type ListSort } from "../lib/list";
import { store, useAppSelector } from "../store";
import { TaskList } from "./TaskList";
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
 * opens a sheet. Saved views and the list arrangement join that same sheet
 * rather than growing a second control, for the same reason.
 *
 * The list itself is TaskList — the same component and the same sections the
 * desktop list uses, rendered as cards because a dense five-column row is not a
 * touch target. This board used to hand-roll its own pinned-first, newest-first
 * ordering, which is how it and the desktop list came to disagree about what
 * order tasks go in.
 */
export function MobileBoard({ tasks: filteredTasks }: { tasks: Task[] }) {
  const projects = useAppSelector((s) => s.projects);
  const tasks = useAppSelector((s) => s.tasks);
  const filter = useAppSelector((s) => s.filter);
  const listOptions = useAppSelector((s) => s.listOptions);
  const savedViews = useAppSelector((s) => s.savedViews);
  const activeView = useAppSelector((s) => s.activeView);
  const [sheetOpen, setSheetOpen] = useState(false);

  const parsed = parseFilter(filter);
  const activeStatus: FilterKey | null = (parsed.status as FilterKey | undefined) ?? null;
  const activeProject = parsed.project ?? null;
  const searchText = textOf(filter);

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
    setSheetOpen(false);
  }
  function setProject(name: string | null) {
    store.setFilter(withToken(filter, PROJECT_TOKEN, name ? `[${name}]` : null));
    setSheetOpen(false);
  }
  function setText(next: string) {
    const status = filter.match(STATUS_TOKEN)?.[0];
    const project = filter.match(PROJECT_TOKEN)?.[0];
    store.setFilter([status, project, next.trim()].filter(Boolean).join(" "));
  }
  function clearAll() {
    store.clearFilter();
  }

  const statusLabel = activeView
    ? activeView
    : activeStatus
      ? FILTERS.find((f) => f.key === activeStatus)!.label
      : "All tasks";
  const filtered =
    activeStatus !== null || activeProject !== null || searchText !== "" || activeView !== "";

  const emptyMessage = filtered
    ? activeStatus && !activeProject && !searchText
      ? EMPTY[activeStatus]
      : "No tasks match that filter."
    : "No tasks yet.";

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
            {filteredTasks.length}
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

      <TaskList
        tasks={filteredTasks}
        options={listOptions}
        variant="card"
        emptyMessage={emptyMessage}
      />

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

          {savedViews.length > 0 && (
            <>
              <Section title="Views" />
              {savedViews.map((view) => (
                <Row
                  key={view.id}
                  label={view.name}
                  selected={activeView === view.name}
                  onClick={() => {
                    void store.applyView(view.name);
                    setSheetOpen(false);
                  }}
                />
              ))}
            </>
          )}

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

          {/* Arrangement lives in this sheet rather than a second control, for
              the same reason everything else does: 390px only fits one. */}
          <Section title="Group by" />
          <Chips
            options={GROUP_BY_OPTIONS}
            value={listOptions.groupBy}
            onChange={(groupBy: ListGroupBy) => store.setListOptions({ ...listOptions, groupBy })}
          />
          <Section title="Sort" />
          <Chips
            options={SORT_OPTIONS}
            value={listOptions.sort}
            onChange={(sort: ListSort) => store.setListOptions({ ...listOptions, sort })}
          />
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
            "h-9 rounded-lg px-3 text-[13px] capitalize",
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
      <span className={cn("min-w-0 truncate", count === undefined && "flex-1")}>{label}</span>
      {count !== undefined && (
        <span className="ml-auto shrink-0 text-[13px] tabular-nums opacity-70">{count}</span>
      )}
      {selected && <Check className="size-4 shrink-0" />}
    </button>
  );
}
