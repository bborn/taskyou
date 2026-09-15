import { useMemo, useState } from "react";
import { AnimatePresence } from "motion/react";
import { Check, ListFilter, X } from "lucide-react";
import type { Column } from "../lib/board";
import { parseFilter } from "../lib/board";
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

/** "Needs you" comes first: the point of the phone is unblocking work you
 * can't see, not browsing the backlog. */
const FILTERS = [
  { key: "blocked", label: "Needs you", token: "blocked" },
  { key: "processing", label: "Running", token: "running" },
  { key: "backlog", label: "Backlog", token: "backlog" },
  { key: "done", label: "Done", token: "done" },
] as const;

type FilterKey = (typeof FILTERS)[number]["key"];

/** With no `is:` token the board opens on what is waiting on you, rather than
 * mixing several hundred finished tasks into the list. */
const DEFAULT_STATUS: FilterKey = "blocked";

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
 */
export function MobileBoard({ columns }: { columns: Column[] }) {
  const projects = useAppSelector((s) => s.projects);
  const tasks = useAppSelector((s) => s.tasks);
  const latestLogs = useAppSelector((s) => s.latestLogs);
  const filter = useAppSelector((s) => s.filter);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);

  const parsed = parseFilter(filter);
  const active: FilterKey = (parsed.status as FilterKey) ?? DEFAULT_STATUS;
  const activeProject = parsed.project ?? null;
  const searchText = textOf(filter);

  const columnTasks = columns.find((c) => c.status === active)?.tasks ?? [];
  const visible = showAll ? columnTasks : columnTasks.slice(0, RENDER_CAP);

  // Counts ignore the dimension they describe: status counts are computed
  // across every status, project counts within the chosen one. Reading either
  // off `columns` would report 0 for whatever is currently filtered out.
  const { statusCounts, projectChips } = useMemo(() => {
    const text = searchText.toLowerCase();
    const matchesText = (t: (typeof tasks)[number]) =>
      !text || t.title.toLowerCase().includes(text) || t.body.toLowerCase().includes(text);

    const byStatus = new Map<string, number>();
    const byProject = new Map<string, number>();
    for (const t of tasks) {
      if (t.status === "archived" || !matchesText(t)) continue;
      const status = t.status === "queued" ? "backlog" : t.status;
      if (!activeProject || t.project === activeProject) {
        byStatus.set(status, (byStatus.get(status) ?? 0) + 1);
      }
      if (status === active) byProject.set(t.project, (byProject.get(t.project) ?? 0) + 1);
    }

    const chips = projects
      .map((project) => ({ project, count: byProject.get(project.name) ?? 0 }))
      .filter((c) => c.count > 0 || c.project.name === activeProject)
      .sort((a, b) => b.count - a.count || a.project.name.localeCompare(b.project.name));

    return { statusCounts: byStatus, projectChips: chips };
  }, [tasks, projects, active, activeProject, searchText]);

  // "All projects" sits above rows counted WITHIN the chosen status, so it has
  // to be that status's total. Summing statusCounts instead totalled every
  // status and showed 964 above children adding up to 97.
  const totalForStatus = statusCounts.get(active) ?? 0;

  // Picking a status or a project is a decision; close the sheet so you land
  // back on the list. Typing is not, so the text field leaves it open.
  function setStatus(key: FilterKey) {
    const token = FILTERS.find((f) => f.key === key)!.token;
    store.setFilter(withToken(filter, STATUS_TOKEN, `is:${token}`));
    setShowAll(false);
    setSheetOpen(false);
  }
  function setProject(name: string | null) {
    store.setFilter(withToken(filter, PROJECT_TOKEN, name ? `[${name}]` : null));
    setShowAll(false);
    setSheetOpen(false);
  }
  function setText(next: string) {
    const keep = filter.match(STATUS_TOKEN)?.[0];
    const project = filter.match(PROJECT_TOKEN)?.[0];
    store.setFilter([keep, project, next.trim()].filter(Boolean).join(" "));
    setShowAll(false);
  }
  function clearAll() {
    store.setFilter("");
    setShowAll(false);
  }

  const activeLabel = FILTERS.find((f) => f.key === active)!.label;
  const narrowed = activeProject !== null || searchText !== "";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* One control: status, project and text in a single field. */}
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <button
          onClick={() => setSheetOpen(true)}
          className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-lg border bg-surface-1 px-3 text-left text-[13px]"
        >
          <ListFilter className="size-4 shrink-0 text-muted-foreground" />
          <span className={cn("shrink-0 font-medium", ACCENT[active])}>{activeLabel}</span>
          <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
            {columnTasks.length}
          </span>
          {activeProject && (
            <span className="min-w-0 truncate text-muted-foreground">· {activeProject}</span>
          )}
          {searchText && <span className="min-w-0 truncate text-muted-foreground">· {searchText}</span>}
        </button>
        {narrowed && (
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
        {columnTasks.length === 0 ? (
          <div className="px-4 py-12 text-center text-sm text-muted-foreground">
            {narrowed ? "No tasks match that filter." : EMPTY[active]}
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
          {FILTERS.map((f) => (
            <Row
              key={f.key}
              label={f.label}
              count={statusCounts.get(f.key) ?? 0}
              selected={active === f.key}
              onClick={() => setStatus(f.key)}
            />
          ))}

          <Section title="Project" />
          <Row
            label="All projects"
            count={totalForStatus}
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
