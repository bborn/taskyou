import { useEffect, useRef, useState } from "react";
import { GitPullRequest, Pin } from "lucide-react";
import type { Task } from "../api/types";
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

function useSpinner(active: boolean): string {
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setFrame((f) => (f + 1) % SPINNER_FRAMES.length), 200);
    return () => clearInterval(id);
  }, [active]);
  return SPINNER_FRAMES[frame];
}

function TaskCard({ task, selected }: { task: Task; selected: boolean }) {
  const { projects, latestLogs } = useAppState();
  const ref = useRef<HTMLDivElement>(null);
  const spinner = useSpinner(task.status === "processing");
  const project = projects.find((p) => p.name === task.project);
  const latest = latestLogs[String(task.id)];

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
        "flex flex-col gap-1 rounded-lg border border-white/[0.07] bg-white/[0.05] px-2.5 py-2 transition-colors hover:bg-white/[0.09] active:cursor-grabbing",
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
        <span className="line-clamp-2 text-[12.5px] leading-snug text-foreground">
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
        {task.pinned && <Pin className="size-3 text-amber-300" />}
        {project && (
          <span
            className="text-[10px] font-medium"
            style={{ color: project.color || "var(--muted-foreground)" }}
          >
            {task.project}
          </span>
        )}
        {isQueued && (
          <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
            queued
          </Badge>
        )}
        {needsInput && (
          <Badge variant="outline" className="h-4 border-status-blocked/60 px-1.5 text-[10px] text-status-blocked">
            needs input
          </Badge>
        )}
        {task.pr_url && (
          <Badge variant="outline" className="h-4 gap-0.5 border-purple-400/40 px-1.5 text-[10px] text-purple-300">
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
}

function BoardColumn({
  column,
  collapsed,
}: {
  column: Column;
  collapsed: boolean;
}) {
  const { selectedTaskId } = useAppState();
  const [dragOver, setDragOver] = useState(false);

  if (collapsed) {
    return (
      <div
        className="flex w-10 shrink-0 flex-col items-center rounded-xl border border-white/[0.06] bg-white/[0.03] py-3"
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
        "flex min-w-[230px] flex-1 flex-col rounded-xl border border-white/[0.06] bg-white/[0.03] transition-colors",
        dragOver && "border-ring/60 bg-white/[0.06]",
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
      <div className="flex items-center gap-2 border-b border-white/[0.06] px-3 py-2.5">
        <span
          className={cn(
            "text-[11px] font-semibold uppercase tracking-wider",
            COLUMN_ACCENT[column.status],
          )}
        >
          {column.label}
        </span>
        <span className="text-[11px] text-muted-foreground">{column.tasks.length}</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-2">
        {column.tasks.length === 0 ? (
          <div className="px-2 py-6 text-center text-xs text-muted-foreground">
            {emptyMessage(column.status)}
          </div>
        ) : (
          column.tasks.map((task) => (
            <TaskCard key={task.id} task={task} selected={task.id === selectedTaskId} />
          ))
        )}
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
