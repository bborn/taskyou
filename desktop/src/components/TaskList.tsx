import { usePersistedToggle } from "../hooks/use-persisted-toggle";
import { memo, useState } from "react";
import { Pin, ChevronDown, ChevronRight } from "lucide-react";
import type { LogLine, Task, TaskStatus } from "../api/types";
import { ageHint, referenceTime, shortDuration } from "../lib/board";
import { buildSections, PINNED_GROUP, type ListSection, type ListOptions } from "../lib/list";
import { store, useAppSelector } from "../store";
import { CardSlot, PRBadge, cardSubLine, useSpinner } from "./Board";
import { cn } from "@/lib/utils";

// The list view: the same board as one flat, sectioned list instead of four
// columns. This is the ONLY list renderer — the phone board uses it too.
//
// The one thing that differs by viewport is the row itself. A dense five-column
// line (dot / id / project / title / badges / age) does not survive 390px, and
// a 13px row is not a touch target, so the phone renders the same sections as
// cards. Everything above the row — which sections exist, what order they are
// in, what is capped — is shared, because that is where the two surfaces used
// to disagree for no reason.
//
// Row height is decided, not configured (parity with the TUI): a row is one
// line, and grows a second only when the task has something live to say — the
// agent's current step, or the stand a blocked task is waiting on. A backlog
// item has nothing to report, so a second line there would buy nothing.

/** How a task is drawn. "row" is the dense desktop line; "card" is the phone's
 * tappable card. */
export type ListVariant = "row" | "card";

/** Rows rendered per section before a "N more" button. The store loads every
 * task including done, so an uncapped Done section is thousands of rows — the
 * kanban column and the phone list both cap at the same number. */
const SECTION_RENDER_CAP = 50;

const STATUS_DOT: Record<string, string> = {
  backlog: "bg-status-backlog",
  queued: "bg-status-processing",
  processing: "bg-status-processing",
  blocked: "bg-status-blocked",
  done: "bg-status-done",
};

const STATUS_TEXT: Record<string, string> = {
  backlog: "text-status-backlog",
  queued: "text-status-processing",
  processing: "text-status-processing",
  blocked: "text-status-blocked",
  done: "text-status-done",
};

/** Just the duration — the status is already said by the dot and the section,
 * so "blocked 28s" would repeat it and wrap the column onto a second line. */
function shortAge(task: Task): string {
  const ref = referenceTime(task);
  return ref ? shortDuration(Math.max(0, Date.now() - ref)) : "";
}

/** Whether a row earns its second line. Mirrors rowHasActivity in the TUI: the
 * test is "does the sub-line say more than the age?", not "is it running?" —
 * the age is already at the right of the first line, so a row that grew just to
 * repeat it would be noise. */
function activityLine(task: Task, latest?: LogLine): string {
  if (task.status !== "processing" && task.status !== "blocked") return "";
  const sub = cardSubLine(task, latest);
  return sub.text && sub.text !== ageHint(task) ? sub.text : "";
}

interface RowProps {
  task: Task;
  selected: boolean;
  projectColor: string;
  latest: LogLine | undefined;
  showProject: boolean;
}

function rowPropsEqual(prev: RowProps, next: RowProps): boolean {
  const a = prev.task;
  const b = next.task;
  return (
    prev.selected === next.selected &&
    prev.projectColor === next.projectColor &&
    prev.showProject === next.showProject &&
    prev.latest?.id === next.latest?.id &&
    a.id === b.id &&
    a.title === b.title &&
    a.status === b.status &&
    a.pinned === b.pinned &&
    a.project === b.project &&
    a.pr_url === b.pr_url &&
    a.pr?.state === b.pr?.state &&
    a.pr?.check_state === b.pr?.check_state &&
    a.pr?.additions === b.pr?.additions &&
    a.pr?.deletions === b.pr?.deletions &&
    a.updated_at === b.updated_at &&
    a.stand === b.stand &&
    a.summary === b.summary
  );
}

const TaskRow = memo(function TaskRow({
  task,
  selected,
  projectColor,
  latest,
  showProject,
}: RowProps) {
  const spinner = useSpinner(task.status === "processing");
  const activity = activityLine(task, latest);

  return (
    <div
      data-task-row={task.id}
      className={cn(
        "group cursor-pointer rounded-md px-2 py-0.5 transition-colors",
        selected ? "bg-accent text-accent-foreground" : "hover:bg-surface-2",
      )}
      onClick={() => store.selectTask(task.id)}
      onDoubleClick={() => store.openDetail(task.id)}
    >
      <div className="flex items-baseline gap-2">
        <span
          className={cn(
            "size-1.5 shrink-0 translate-y-[-1px] rounded-full",
            STATUS_DOT[task.status] ?? "bg-muted-foreground",
          )}
          aria-label={task.status}
        />
        <span className="w-12 shrink-0 text-right font-mono text-[11px] text-muted-foreground">
          #{task.id}
        </span>
        {showProject && (
          <span
            className="w-28 shrink-0 truncate text-[11px] font-medium"
            style={{ color: projectColor }}
          >
            {task.project}
          </span>
        )}
        <span className="min-w-0 flex-1 truncate text-[13px]" title={task.title}>
          {task.title}
        </span>
        <span className="flex shrink-0 items-center gap-1.5">
          {task.status === "processing" && (
            <span className={cn("font-mono text-[11px]", STATUS_TEXT.processing)}>{spinner}</span>
          )}
          {task.pinned && <Pin className="size-3 text-amber-500" aria-label="pinned" />}
          <PRBadge task={task} />
          <span
            className="w-10 text-right font-mono text-[10px] text-muted-foreground"
            title={ageHint(task)}
          >
            {shortAge(task)}
          </span>
        </span>
      </div>
      {activity && (
        <div
          className={cn(
            // Indent to where the title starts, so the two lines read as one row.
            "truncate text-[11px]",
            task.status === "blocked" ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground",
          )}
          style={{ paddingLeft: showProject ? "12.5rem" : "5.5rem" }}
          title={activity}
        >
          {activity}
        </div>
      )}
    </div>
  );
}, rowPropsEqual);

/** One section, capped. Two things are never hidden behind the cap: the pinned
 * section (a pinned task falling out of view defeats the point of pinning) and
 * the selected task (the keyboard can move onto a row the cap would hide). */
function Section({
  section,
  selectedTaskId,
  showProject,
  showHeader,
  variant,
  projectColorFor,
  latestFor,
  groupBy,
}: {
  section: ListSection;
  groupBy: ListOptions["groupBy"];
  selectedTaskId: number | null;
  showProject: boolean;
  showHeader: boolean;
  variant: ListVariant;
  projectColorFor: (task: Task) => string;
  latestFor: (task: Task) => LogLine | undefined;
}) {
  const [showAll, setShowAll] = useState(false);
  const [collapsed, setCollapsed] = usePersistedToggle(`ty:list:${groupBy}:${section.key}:collapsed`, false);
  const selectedBeyondCap =
    section.tasks.findIndex((t) => t.id === selectedTaskId) >= SECTION_RENDER_CAP;
  const uncapped = showAll || selectedBeyondCap || section.key === PINNED_GROUP;
  const visible = uncapped ? section.tasks : section.tasks.slice(0, SECTION_RENDER_CAP);
  const hidden = section.tasks.length - visible.length;

  return (
    <div className={variant === "card" ? "flex flex-col gap-2" : undefined}>
      {showHeader && section.title && (
        <button className="w-full min-h-11 text-left" aria-expanded={!collapsed} onClick={() => setCollapsed(!collapsed)}>
          <SectionHeader title={section.title} status={section.status} count={section.tasks.length} collapsed={collapsed} />
        </button>
      )}
      {!collapsed && visible.map((task) =>
        variant === "card" ? (
          <CardSlot
            key={task.id}
            task={task}
            selected={false}
            tapToOpen
            showProject={showProject}
            projectColor={projectColorFor(task)}
            latest={latestFor(task)}
          />
        ) : (
          <TaskRow
            key={task.id}
            task={task}
            selected={task.id === selectedTaskId}
            projectColor={projectColorFor(task)}
            latest={latestFor(task)}
            showProject={showProject}
          />
        ),
      )}
      {!collapsed && hidden > 0 && (
        <button
          className={cn(
            "w-full rounded-md text-center text-muted-foreground",
            variant === "card"
              ? "py-3 text-[13px] active:bg-surface-2"
              : "py-1.5 text-[11px] hover:bg-surface-2",
          )}
          onClick={() => setShowAll(true)}
        >
          {hidden} more…
        </button>
      )}
    </div>
  );
}

function SectionHeader({
  title,
  status,
  count,
  collapsed,
}: {
  title: string;
  status: TaskStatus | "";
  count: number;
  collapsed: boolean;
}) {
  return (
    <div className="flex items-center gap-2 px-2 py-2">
      {collapsed ? <ChevronRight className="size-4 shrink-0" /> : <ChevronDown className="size-4 shrink-0" />}
      <span
        className={cn(
          "min-w-0 truncate text-[11px] font-semibold uppercase tracking-wider",
          status ? STATUS_TEXT[status] : "text-amber-500",
        )}
      >
        {title}
      </span>
      <span className="h-px flex-1 bg-border" />
      <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{count}</span>
    </div>
  );
}

export function TaskList({
  tasks,
  options,
  variant = "row",
  emptyMessage = "No tasks match this filter.",
}: {
  tasks: Task[];
  options: ListOptions;
  variant?: ListVariant;
  emptyMessage?: string;
}) {
  const selectedTaskId = useAppSelector((s) => s.selectedTaskId);
  const projects = useAppSelector((s) => s.projects);
  const latestLogs = useAppSelector((s) => s.latestLogs);

  const sections = buildSections(tasks, options);
  // Grouping by project makes the per-row project column pure repetition of the
  // section header, so it comes off and the width goes to titles.
  const showProject = options.groupBy !== "project";
  // A lone section's header says nothing the filter above it has not already
  // said — "Blocked" under a control that reads "Needs you · 5".
  const showHeaders = sections.length > 1 || options.groupBy !== "none";

  if (sections.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center px-4 py-12 text-center text-sm text-muted-foreground">
        {emptyMessage}
      </div>
    );
  }

  return (
    <div
      className={cn(
        "flex-1 overflow-y-auto",
        variant === "card"
          ? "flex flex-col gap-2 overscroll-contain px-3 pt-2 pb-[max(1.5rem,env(safe-area-inset-bottom))]"
          : "rounded-xl border bg-surface-1 p-2",
      )}
    >
      {sections.map((section) => (
        <Section
          key={`${options.groupBy}:${section.key || "all"}`}
          section={section}
          groupBy={options.groupBy}
          selectedTaskId={selectedTaskId}
          showProject={showProject}
          showHeader={showHeaders}
          variant={variant}
          projectColorFor={(task) =>
            projects.find((p) => p.name === task.project)?.color || "var(--muted-foreground)"
          }
          latestFor={(task) => latestLogs[String(task.id)]}
        />
      ))}
    </div>
  );
}
