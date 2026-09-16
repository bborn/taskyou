import { memo, useState } from "react";
import { Pin } from "lucide-react";
import type { LogLine, Task, TaskStatus } from "../api/types";
import { ageHint, referenceTime, shortDuration } from "../lib/board";
import { buildSections, PINNED_GROUP, type ListSection, type ListOptions } from "../lib/list";
import { store, useAppSelector } from "../store";
import { PRBadge, cardSubLine, useSpinner } from "./Board";
import { cn } from "@/lib/utils";

// The list view: the same board, one line per task instead of four columns.
//
// Row height is decided, not configured (parity with the TUI): a row is one
// line, and grows a second only when the task has something live to say — the
// agent's current step, or the stand a blocked task is waiting on. A backlog
// item has nothing to report, so a second line there would buy nothing.

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
  projectColorFor,
  latestFor,
}: {
  section: ListSection;
  selectedTaskId: number | null;
  showProject: boolean;
  projectColorFor: (task: Task) => string;
  latestFor: (task: Task) => LogLine | undefined;
}) {
  const [showAll, setShowAll] = useState(false);
  const selectedBeyondCap =
    section.tasks.findIndex((t) => t.id === selectedTaskId) >= SECTION_RENDER_CAP;
  const uncapped = showAll || selectedBeyondCap || section.key === PINNED_GROUP;
  const visible = uncapped ? section.tasks : section.tasks.slice(0, SECTION_RENDER_CAP);
  const hidden = section.tasks.length - visible.length;

  return (
    <div>
      {section.title && (
        <SectionHeader title={section.title} status={section.status} count={section.tasks.length} />
      )}
      {visible.map((task) => (
        <TaskRow
          key={task.id}
          task={task}
          selected={task.id === selectedTaskId}
          projectColor={projectColorFor(task)}
          latest={latestFor(task)}
          showProject={showProject}
        />
      ))}
      {hidden > 0 && (
        <button
          className="w-full rounded-md py-1.5 text-center text-[11px] text-muted-foreground hover:bg-surface-2"
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
}: {
  title: string;
  status: TaskStatus | "";
  count: number;
}) {
  return (
    <div className="flex items-center gap-2 px-2 pb-1 pt-3 first:pt-1">
      <span
        className={cn(
          "text-[11px] font-semibold uppercase tracking-wider",
          status ? STATUS_TEXT[status] : "text-amber-500",
        )}
      >
        {title}
      </span>
      <span className="h-px flex-1 bg-border" />
      <span className="font-mono text-[11px] text-muted-foreground">{count}</span>
    </div>
  );
}

export function TaskList({ tasks, options }: { tasks: Task[]; options: ListOptions }) {
  const selectedTaskId = useAppSelector((s) => s.selectedTaskId);
  const projects = useAppSelector((s) => s.projects);
  const latestLogs = useAppSelector((s) => s.latestLogs);

  const sections = buildSections(tasks, options);
  // Grouping by project makes the per-row project column pure repetition of the
  // section header, so it comes off and the width goes to titles.
  const showProject = options.groupBy !== "project";

  if (sections.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
        No tasks match this filter.
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto rounded-xl border bg-surface-1 p-2">
      {sections.map((section) => (
        <Section
          key={section.key || "all"}
          section={section}
          selectedTaskId={selectedTaskId}
          showProject={showProject}
          projectColorFor={(task) =>
            projects.find((p) => p.name === task.project)?.color || "var(--muted-foreground)"
          }
          latestFor={(task) => latestLogs[String(task.id)]}
        />
      ))}
    </div>
  );
}
