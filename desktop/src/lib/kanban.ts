import type { Task } from "../api/types";
import { buildColumns, type Column } from "./board";
import { compareWithin, type ListOptions } from "./list";

/** Kanban shares the list's grouping and ordering preferences. Pinned tasks
 * lead their own column rather than leaving their project for a Pinned column. */
export function buildKanbanColumns(tasks: Task[], options: ListOptions): Column[] {
  const visible = tasks.filter((task) => task.status !== "archived");
  let columns: Column[];
  if (options.groupBy === "status") {
    columns = buildColumns(visible);
  } else if (options.groupBy === "none") {
    columns = [{ key: "all", status: "", label: "All tasks", tasks: [...visible] }];
  } else {
    const groups = new Map<string, Task[]>();
    for (const task of visible) {
      const group = groups.get(task.project) ?? [];
      group.push(task);
      groups.set(task.project, group);
    }
    columns = Array.from(groups, ([project, tasks]) => ({
      key: `project:${project}`, status: "" as const, project,
      label: project || "No project", tasks,
    })).sort((a, b) => a.label.localeCompare(b.label));
    if (!columns.length) columns = [{ key: "empty", status: "", label: "Projects", tasks: [] }];
  }
  for (const column of columns) {
    // Keep the established status-board urgency order (queued folds into backlog).
    if (options.groupBy === "status" && options.sort === "urgency") continue;
    column.tasks.sort((a, b) => Number(b.pinned) - Number(a.pinned) || compareWithin(a, b, options.sort) || b.id - a.id);
  }
  return columns;
}
