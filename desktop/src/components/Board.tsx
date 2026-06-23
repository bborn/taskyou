import { memo, useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { Check, Clock, GitMerge, GitPullRequest, GitPullRequestClosed, Pin, X } from "lucide-react";
import { api } from "../api/client";
import type { LogLine, PRStatus, Task } from "../api/types";
import { ageHint, type Column } from "../lib/board";
import { store, useAppSelector } from "../store";
import { checkEnvironment, inTauri } from "../tauri";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";

const SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

/** Cards rendered per column; the rest collapse into a "+N more" footer
 * (matches the TUI's hidden-done behavior and keeps huge boards instant). */
const COLUMN_RENDER_CAP = 50;

const CARD_TRANSITION = {
  duration: 0.14,
  ease: "easeOut" as const,
  layout: { duration: 0.2 },
};

const COLUMN_ACCENT: Record<string, string> = {
  backlog: "text-status-backlog",
  processing: "text-status-processing",
  blocked: "text-status-blocked",
  done: "text-status-done",
};

const COLUMN_DOT: Record<string, string> = {
  backlog: "bg-status-backlog",
  processing: "bg-status-processing",
  blocked: "bg-status-blocked",
  done: "bg-status-done",
};

/** Per-state visual treatment for the PR badge (icon + border/text color),
 * mirroring the TUI's PRStatusBadge palette. */
const PR_STATE_STYLE: Record<
  PRStatus["state"],
  { Icon: typeof GitPullRequest; className: string; label: string }
> = {
  open: {
    Icon: GitPullRequest,
    className: "border-emerald-400/40 text-emerald-600 dark:text-emerald-300",
    label: "open",
  },
  draft: {
    Icon: GitPullRequest,
    className: "border-muted-foreground/40 text-muted-foreground",
    label: "draft",
  },
  merged: {
    Icon: GitMerge,
    className: "border-purple-400/40 text-purple-600 dark:text-purple-300",
    label: "merged",
  },
  closed: {
    Icon: GitPullRequestClosed,
    className: "border-red-400/40 text-red-600 dark:text-red-300",
    label: "closed",
  },
};

/** CI check rollup indicator appended to the badge. Conflicting merges read as a
 * failure (parity with the TUI, where conflicts outrank check status). */
function CheckMark({ pr }: { pr: PRStatus }) {
  if (pr.state === "merged" || pr.state === "closed") return null;
  if (pr.mergeable === "CONFLICTING") {
    return <X className="size-2.5 text-red-500" aria-label="merge conflicts" />;
  }
  switch (pr.check_state) {
    case "passing":
      return <Check className="size-2.5 text-emerald-500" aria-label="checks passing" />;
    case "failing":
      return <X className="size-2.5 text-red-500" aria-label="checks failing" />;
    case "pending":
      return <Clock className="size-2.5 text-amber-500" aria-label="checks running" />;
    default:
      return null;
  }
}

/** Live PR badge: state, CI checks, and diff size. Falls back to a bare PR chip
 * for legacy rows that have a URL but no cached PR state yet. */
function PRBadge({ task }: { task: Task }) {
  const pr = task.pr;
  if (!pr) {
    if (!task.pr_url) return null;
    return (
      <Badge
        variant="outline"
        className="h-4 gap-0.5 border-purple-400/40 px-1.5 text-[10px] text-purple-600 dark:text-purple-300"
      >
        <GitPullRequest className="size-2.5" />
        {task.pr_number ? `#${task.pr_number}` : "PR"}
      </Badge>
    );
  }
  const style = PR_STATE_STYLE[pr.state] ?? PR_STATE_STYLE.open;
  const { Icon } = style;
  const title = `PR #${pr.number} — ${style.label}${pr.check_state ? ` · checks ${pr.check_state}` : ""}`;
  return (
    <Badge
      variant="outline"
      className={cn("h-4 gap-0.5 px-1.5 text-[10px]", style.className)}
      title={title}
    >
      <Icon className="size-2.5" />
      {pr.number ? `#${pr.number}` : "PR"}
      <CheckMark pr={pr} />
      {(pr.additions > 0 || pr.deletions > 0) && (
        <span className="ml-0.5 font-mono text-muted-foreground">
          {pr.additions > 0 && <span className="text-emerald-600 dark:text-emerald-400">+{pr.additions}</span>}
          {pr.deletions > 0 && <span className="ml-0.5 text-red-600 dark:text-red-400">−{pr.deletions}</span>}
        </span>
      )}
    </Badge>
  );
}

function useSpinner(active: boolean): string {
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setFrame((f) => (f + 1) % SPINNER_FRAMES.length), 200);
    return () => clearInterval(id);
  }, [active]);
  return SPINNER_FRAMES[frame];
}

interface CardProps {
  task: Task;
  selected: boolean;
  projectColor: string;
  latest: LogLine | undefined;
}

/** Field-level equality: API refreshes return fresh objects every time, so
 * reference checks would re-render the whole board on every SSE tick. */
function cardPropsEqual(prev: CardProps, next: CardProps): boolean {
  const a = prev.task;
  const b = next.task;
  return (
    prev.selected === next.selected &&
    prev.projectColor === next.projectColor &&
    prev.latest?.id === next.latest?.id &&
    a.id === b.id &&
    a.title === b.title &&
    a.status === b.status &&
    a.pinned === b.pinned &&
    a.pr_url === b.pr_url &&
    a.pr_number === b.pr_number &&
    a.pr?.state === b.pr?.state &&
    a.pr?.check_state === b.pr?.check_state &&
    a.pr?.mergeable === b.pr?.mergeable &&
    a.pr?.additions === b.pr?.additions &&
    a.pr?.deletions === b.pr?.deletions &&
    a.executor === b.executor &&
    a.project === b.project &&
    a.updated_at === b.updated_at
  );
}

const CardSlot = memo(function CardSlot({ task, selected, projectColor, latest }: CardProps) {
  const ref = useRef<HTMLDivElement>(null);
  const spinner = useSpinner(task.status === "processing");

  useEffect(() => {
    if (selected) ref.current?.scrollIntoView({ block: "nearest" });
  }, [selected]);

  const isQueued = task.status === "queued";
  const needsInput = task.status === "blocked";

  return (
    <motion.div
      layout
      layoutId={`task-${task.id}`}
      initial={{ opacity: 0, scale: 0.97 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.97 }}
      transition={CARD_TRANSITION}
    >
      <div
        ref={ref}
        draggable
        onDragStart={(e) => {
          e.dataTransfer.setData("text/x-task-id", String(task.id));
          e.dataTransfer.effectAllowed = "move";
          store.selectTask(task.id);
        }}
        className={cn(
          "group flex flex-col gap-1 rounded-lg border bg-card px-2.5 py-2 shadow-xs transition-[box-shadow,border-color,background-color] duration-100",
          "hover:shadow-md hover:border-foreground/15",
          "active:cursor-grabbing",
          selected && "border-ring ring-1 ring-ring",
        )}
        onClick={() => store.selectTask(task.id)}
        onDoubleClick={() => store.openDetail(task.id)}
      >
        <div className="flex items-baseline gap-1.5">
          {task.status === "processing" && (
            <span className="w-3 shrink-0 font-mono text-status-processing">{spinner}</span>
          )}
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground">#{task.id}</span>
          <span className="line-clamp-2 text-[12.5px] leading-snug">
            {task.title || "(untitled)"}
          </span>
        </div>
        <div className="truncate text-[11px] text-muted-foreground">
          {latest && (task.status === "processing" || task.status === "blocked") ? (
            <span title={latest.content}>{latest.content}</span>
          ) : (
            <span>{ageHint(task)}</span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {task.pinned && <Pin className="size-3 text-amber-500 dark:text-amber-300" />}
          {task.project && (
            <span className="text-[10px] font-medium" style={{ color: projectColor }}>
              {task.project}
            </span>
          )}
          {isQueued && (
            <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
              queued
            </Badge>
          )}
          {needsInput && (
            <Badge
              variant="outline"
              className="h-4 border-status-blocked/60 px-1.5 text-[10px] text-status-blocked"
            >
              needs input
            </Badge>
          )}
          <PRBadge task={task} />
          {task.executor && task.executor !== "claude" && (
            <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
              {task.executor}
            </Badge>
          )}
        </div>
      </div>
    </motion.div>
  );
}, cardPropsEqual);

function BoardColumn({ column, collapsed }: { column: Column; collapsed: boolean }) {
  const selectedTaskId = useAppSelector((s) => s.selectedTaskId);
  const projects = useAppSelector((s) => s.projects);
  const latestLogs = useAppSelector((s) => s.latestLogs);
  const [dragOver, setDragOver] = useState(false);
  const [showAll, setShowAll] = useState(false);

  if (collapsed) {
    return (
      <div
        className="flex w-10 shrink-0 flex-col items-center rounded-xl border bg-surface-1 py-3 transition-colors hover:bg-surface-2"
        onClick={() => store.toggleCollapsed(column.status as "backlog" | "done")}
      >
        <span
          className={cn(
            "text-[11px] font-semibold uppercase tracking-wider [writing-mode:vertical-rl]",
            COLUMN_ACCENT[column.status],
          )}
        >
          {column.label} · {column.tasks.length}
        </span>
      </div>
    );
  }

  // Auto-expand when keyboard selection walks past the cap.
  const selectedBeyondCap =
    !showAll &&
    selectedTaskId !== null &&
    column.tasks.findIndex((t) => t.id === selectedTaskId) >= COLUMN_RENDER_CAP;
  const effectiveShowAll = showAll || selectedBeyondCap;
  const visible = effectiveShowAll ? column.tasks : column.tasks.slice(0, COLUMN_RENDER_CAP);
  const hidden = column.tasks.length - visible.length;

  return (
    <div
      className={cn(
        "flex min-w-[230px] flex-1 flex-col rounded-xl border bg-surface-1 transition-colors duration-100",
        dragOver && "border-ring/60 bg-surface-2 ring-1 ring-ring/40",
      )}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("text/x-task-id")) {
          e.preventDefault();
          e.dataTransfer.dropEffect = "move";
          setDragOver(true);
        }
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragOver(false);
        const id = parseInt(e.dataTransfer.getData("text/x-task-id"), 10);
        if (id) store.moveTaskToColumn(id, column.status);
      }}
    >
      <div className="flex items-center gap-2 border-b px-3 py-2.5">
        <span className={cn("size-1.5 rounded-full", COLUMN_DOT[column.status])} />
        <span
          className={cn(
            "text-[11px] font-semibold uppercase tracking-wider",
            COLUMN_ACCENT[column.status],
          )}
        >
          {column.label}
        </span>
        <span className="rounded-full bg-surface-3 px-1.5 text-[10px] tabular-nums text-muted-foreground">
          {column.tasks.length}
        </span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-2">
        {column.tasks.length === 0 ? (
          <div className="px-2 py-6 text-center text-xs text-muted-foreground">
            {emptyMessage(column.status)}
          </div>
        ) : (
          <AnimatePresence initial={false}>
            {visible.map((task) => (
              <CardSlot
                key={task.id}
                task={task}
                selected={task.id === selectedTaskId}
                projectColor={
                  projects.find((p) => p.name === task.project)?.color || "var(--muted-foreground)"
                }
                latest={latestLogs[String(task.id)]}
              />
            ))}
          </AnimatePresence>
        )}
        {hidden > 0 && (
          <button
            className="rounded-md py-1.5 text-center text-[11px] text-muted-foreground hover:bg-surface-2 hover:text-foreground"
            onClick={() => setShowAll(true)}
          >
            {hidden} more…
          </button>
        )}
      </div>
    </div>
  );
}

/** Empty-board confidence beat: a fresh install looks bare, so show which
 * executor CLIs were detected on this machine. Renders nothing until the
 * check resolves with at least one agent. */
function DetectedAgents() {
  const [agents, setAgents] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      // The Tauri shell probes the machine directly; in the browser the
      // server reports which executors it found on its side.
      const names = inTauri()
        ? (await checkEnvironment()).executors.filter((e) => e.path !== null).map((e) => e.name)
        : (await api.listExecutors()).filter((e) => e.available).map((e) => e.name);
      if (!cancelled) setAgents(names);
    };
    load().catch(() => {}); // best-effort: stay quiet when detection fails
    return () => {
      cancelled = true;
    };
  }, []);

  if (agents.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center justify-center gap-1.5 px-3.5 pb-4 text-[11px] text-muted-foreground">
      <span>Detected agents on this machine:</span>
      {agents.map((name) => (
        <Badge key={name} variant="outline" className="h-4 px-1.5 text-[10px]">
          {name}
        </Badge>
      ))}
    </div>
  );
}

export function Board({
  columns,
  collapsed,
}: {
  columns: Column[];
  collapsed: { backlog: boolean; done: boolean };
}) {
  const noTasks = useAppSelector((s) => s.tasks.length === 0);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1 gap-3 overflow-x-auto p-3.5">
        {columns.map((column) => (
          <BoardColumn
            key={column.status}
            column={column}
            collapsed={
              (column.status === "backlog" && collapsed.backlog) ||
              (column.status === "done" && collapsed.done)
            }
          />
        ))}
      </div>
      {noTasks && <DetectedAgents />}
    </div>
  );
}

function emptyMessage(status: string): string {
  switch (status) {
    case "backlog":
      return "No tasks — press n to create one";
    case "processing":
      return "Nothing running — drag a card here to execute it";
    case "blocked":
      return "Nothing waiting on you";
    case "done":
      return "Nothing finished yet";
    default:
      return "Empty";
  }
}
