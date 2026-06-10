import { useEffect, useRef, useState } from "react";
import type { Task } from "../api/types";
import { ageHint, type Column } from "../lib/board";
import { store, useAppState } from "../store";

const SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

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
      className={`card ${selected ? "selected" : ""}`}
      onClick={() => store.selectTask(task.id)}
      onDoubleClick={() => store.openDetail(task.id)}
    >
      <div className="card-title-row">
        {task.status === "processing" && <span className="spinner">{spinner}</span>}
        <span className="card-id">#{task.id}</span>
        <span className="card-title">{task.title || "(untitled)"}</span>
      </div>
      <div className="card-sub">
        {latest && (task.status === "processing" || task.status === "blocked") ? (
          <span className="activity" title={latest.content}>
            {latest.content}
          </span>
        ) : (
          <span>{ageHint(task)}</span>
        )}
      </div>
      <div className="card-badges">
        {task.pinned && <span className="badge pinned">📌</span>}
        {project && (
          <span className="badge project" style={{ color: project.color || "var(--text-dim)" }}>
            {task.project}
          </span>
        )}
        {isQueued && <span className="badge">queued</span>}
        {needsInput && <span className="badge needs-input">needs input</span>}
        {task.pr_url && <span className="badge pr">PR{task.pr_number ? ` #${task.pr_number}` : ""}</span>}
        {task.executor && task.executor !== "claude" && <span className="badge">{task.executor}</span>}
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
  const { selectedTaskId } = useAppState();

  return (
    <div className="board">
      {columns.map((column) => {
        const isCollapsed =
          (column.status === "backlog" && collapsed.backlog) ||
          (column.status === "done" && collapsed.done);
        if (isCollapsed) {
          return (
            <div
              key={column.status}
              className="column collapsed"
              onClick={() => store.toggleCollapsed(column.status as "backlog" | "done")}
            >
              <div className={`column-header c-${column.status}`}>
                {column.label} <span className="count">{column.tasks.length}</span>
              </div>
            </div>
          );
        }
        return (
          <div key={column.status} className="column">
            <div className={`column-header c-${column.status}`}>
              {column.label} <span className="count">{column.tasks.length}</span>
            </div>
            <div className="column-body">
              {column.tasks.length === 0 ? (
                <div className="column-empty">{emptyMessage(column.status)}</div>
              ) : (
                column.tasks.map((task) => (
                  <TaskCard key={task.id} task={task} selected={task.id === selectedTaskId} />
                ))
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function emptyMessage(status: string): string {
  switch (status) {
    case "backlog":
      return "No tasks — press n to create one";
    case "processing":
      return "Nothing running";
    case "blocked":
      return "Nothing waiting on you";
    case "done":
      return "Nothing finished yet";
    default:
      return "Empty";
  }
}
