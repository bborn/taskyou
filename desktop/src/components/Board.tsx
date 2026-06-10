import { memo, useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { GitPullRequest, Pin } from "lucide-react";
import type { LogLine, Task } from "../api/types";
import { ageHint, type Column } from "../lib/board";
import { store, useAppState } from "../store";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";

const SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

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

function useSpinner(active: boolean): string {
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setFrame((f) => (f + 1) % SPINNER_FRAMES.length), 200);
    return () => clearInterval(id);
  }, [active]);
  return SPINNER_FRAMES[frame];
}

const TaskCard = memo(function TaskCard({
  task,
  selected,
  projectColor,
  latest,
}: {
  task: Task;
  selected: boolean;
  projectColor: string;
  latest: LogLine | undefined;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const spinner = useSpinner(task.status === "processing");

  useEffect(() => {
    if (selected) ref.current?.scrollIntoView({ block: "nearest" });
  }, [selected]);

  const isQueued = task.status === "queued";
  const needsInput = task.status === "blocked";

  return (
    <div
      ref={ref}
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData("text/x-task-id", String(task.id));
        e.dataTransfer.effectAllowed = "move";
        store.selectTask(task.id);
      }}
      className={cn(
        "group flex flex-col gap-1 rounded-lg border bg-card px-2.5 py-2 shadow-xs transition-all duration-150",
        "hover:-translate-y-px hover:shadow-md hover:border-foreground/15",
        "active:translate-y-0 active:shadow-xs active:cursor-grabbing",
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
        {task.pr_url && (
          <Badge
            variant="outline"
            className="h-4 gap-0.5 border-purple-400/40 px-1.5 text-[10px] text-purple-600 dark:text-purple-300"
          >
            <GitPullRequest className="size-2.5" />
            {task.pr_number ? `#${task.pr_number}` : "PR"}
          </Badge>
        )}
        {task.executor && task.executor !== "claude" && (
          <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
            {task.executor}
          </Badge>
        )}
      </div>
    </div>
  );
});

function BoardColumn({ column, collapsed }: { column: Column; collapsed: boolean }) {
  const { selectedTaskId, projects, latestLogs } = useAppState();
  const [dragOver, setDragOver] = useState(false);

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

  return (
    <div
      className={cn(
        "flex min-w-[230px] flex-1 flex-col rounded-xl border bg-surface-1 transition-all duration-150",
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
        <AnimatePresence initial={false}>
          {column.tasks.length === 0 ? (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="px-2 py-6 text-center text-xs text-muted-foreground"
            >
              {emptyMessage(column.status)}
            </motion.div>
          ) : (
            column.tasks.map((task) => (
              <motion.div
                key={task.id}
                layout
                layoutId={`task-${task.id}`}
                initial={{ opacity: 0, scale: 0.97 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.97 }}
                transition={{ duration: 0.16, ease: "easeOut", layout: { duration: 0.22 } }}
              >
                <TaskCard
                  task={task}
                  selected={task.id === selectedTaskId}
                  projectColor={
                    projects.find((p) => p.name === task.project)?.color || "var(--muted-foreground)"
                  }
                  latest={latestLogs[String(task.id)]}
                />
              </motion.div>
            ))
          )}
        </AnimatePresence>
      </div>
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
  return (
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
