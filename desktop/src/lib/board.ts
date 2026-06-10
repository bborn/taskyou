import type { Task, TaskStatus } from "../api/types";
import { fuzzyMatches } from "./fuzzy";

export interface Column {
  status: TaskStatus;
  label: string;
  tasks: Task[];
}

export const COLUMN_DEFS: { status: TaskStatus; label: string }[] = [
  { status: "backlog", label: "Backlog" },
  { status: "processing", label: "In Progress" },
  { status: "blocked", label: "Blocked" },
  { status: "done", label: "Done" },
];

function referenceTime(task: Task): number {
  const t = (s?: string) => (s ? Date.parse(s) : 0);
  switch (task.status) {
    case "processing":
      return t(task.started_at) || t(task.updated_at);
    case "done":
      return t(task.completed_at) || t(task.updated_at);
    case "backlog":
      return t(task.created_at);
    default:
      return t(task.updated_at);
  }
}

export interface ParsedFilter {
  taskId?: number;
  project?: string;
  text?: string;
}

// Filter grammar (parity with the TUI): `#123` selects a task id,
// `[project]` fuzzy-matches a project name, remaining text matches title/body.
export function parseFilter(filter: string): ParsedFilter {
  const parsed: ParsedFilter = {};
  let rest = filter.trim();

  const idMatch = rest.match(/#(\d+)/);
  if (idMatch) {
    parsed.taskId = Number(idMatch[1]);
    rest = rest.replace(idMatch[0], "").trim();
  }
  const projectMatch = rest.match(/\[([^\]]*)\]?/);
  if (projectMatch && projectMatch[1]) {
    parsed.project = projectMatch[1];
    rest = rest.replace(projectMatch[0], "").trim();
  }
  if (rest) parsed.text = rest;
  return parsed;
}

export function applyFilter(tasks: Task[], filter: string, projectNames: string[]): Task[] {
  const parsed = parseFilter(filter);
  if (parsed.taskId !== undefined) {
    return tasks.filter((t) => t.id === parsed.taskId);
  }
  let result = tasks;
  if (parsed.project) {
    const matching = new Set(projectNames.filter((p) => fuzzyMatches(parsed.project!, p)));
    result = result.filter((t) => matching.has(t.project));
  }
  if (parsed.text) {
    const text = parsed.text.toLowerCase();
    result = result.filter(
      (t) => t.title.toLowerCase().includes(text) || t.body.toLowerCase().includes(text),
    );
  }
  return result;
}

export function buildColumns(tasks: Task[]): Column[] {
  const grouped = new Map<TaskStatus, Task[]>();
  for (const task of tasks) {
    if (task.status === "archived") continue;
    // Fold queued into the backlog column (matches board API semantics).
    const status: TaskStatus = task.status === "queued" ? "backlog" : task.status;
    const list = grouped.get(status) ?? [];
    list.push(task);
    grouped.set(status, list);
  }

  return COLUMN_DEFS.map(({ status, label }) => {
    const columnTasks = (grouped.get(status) ?? []).slice();
    columnTasks.sort((a, b) => {
      if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
      return referenceTime(b) - referenceTime(a);
    });
    return { status, label, tasks: columnTasks };
  });
}

export function ageHint(task: Task): string {
  const ref = referenceTime(task);
  if (!ref) return "";
  const delta = Math.max(0, Date.now() - ref);
  const dur = shortDuration(delta);
  switch (task.status) {
    case "processing":
      return `running ${dur}`;
    case "blocked":
      return `blocked ${dur}`;
    case "queued":
      return `queued ${dur}`;
    case "backlog":
      return `created ${dur} ago`;
    case "done":
      return `done ${dur} ago`;
    default:
      return dur;
  }
}

export function shortDuration(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h${m % 60 ? `${m % 60}m` : ""}`;
  const d = Math.floor(h / 24);
  return `${d}d${h % 24 ? `${h % 24}h` : ""}`;
}
