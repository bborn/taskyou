import test from "node:test";
import assert from "node:assert/strict";
import type { Task, TaskStatus } from "../api/types.ts";
import { buildColumns, findTaskPosition } from "./board.ts";

function task(id: number, status: TaskStatus, extra: Partial<Task> = {}): Task {
  return {
    id,
    title: `task ${id}`,
    body: "",
    status,
    type: "code",
    project: "demo",
    executor: "claude",
    pinned: false,
    tags: "",
    permission_mode: "",
    branch_name: "",
    has_executor: false,
    pr_url: "",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...extra,
  } as Task;
}

test("every column carries a short label the phone tab strip can fit", () => {
  const columns = buildColumns([]);
  assert.deepEqual(
    columns.map((c) => c.shortLabel),
    ["Backlog", "Running", "Blocked", "Done"],
  );
  // "In Progress" is the only label wide enough to break a four-tab row.
  assert.equal(columns[1].label, "In Progress");
});

test("findTaskPosition locates a task by column and row", () => {
  const columns = buildColumns([
    task(1, "backlog"),
    task(2, "blocked"),
    task(3, "backlog", { pinned: true }),
  ]);

  // Pinned tasks sort first, so #3 leads the backlog column.
  assert.deepEqual(findTaskPosition(columns, 3), { col: 0, row: 0 });
  assert.deepEqual(findTaskPosition(columns, 1), { col: 0, row: 1 });
  assert.deepEqual(findTaskPosition(columns, 2), { col: 2, row: 0 });
});

test("findTaskPosition returns null for no selection or a filtered-out task", () => {
  const columns = buildColumns([task(1, "backlog")]);
  assert.equal(findTaskPosition(columns, null), null);
  assert.equal(findTaskPosition(columns, 99), null);
  // An archived task is not on the board at all.
  assert.equal(findTaskPosition(buildColumns([task(7, "archived")]), 7), null);
});

test("a queued task is found in the backlog column it renders in", () => {
  // The phone board picks its active tab from this position; if queued tasks
  // reported no position, opening one from search would leave the wrong tab up.
  const columns = buildColumns([task(4, "queued")]);
  assert.deepEqual(findTaskPosition(columns, 4), { col: 0, row: 0 });
});
