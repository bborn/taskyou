import assert from "node:assert/strict";
import test from "node:test";
import { mergeRecentLogs, RECENT_LOG_LIMIT } from "./logs.ts";

const line = (id: number) => ({ id, line_type: "output", content: `${id}`, created_at: "2026-09-06T00:00:00Z" });

test("replayed and out-of-order batches retain each newest log once", () => {
  const result = mergeRecentLogs([line(2), line(3)], [line(1), line(3), line(4)]);
  assert.deepEqual(result.map((l) => l.id), [1, 2, 3, 4]);
});

test("long sessions retain a bounded recent window without mutating prior state", () => {
  const prior = [line(1)];
  const result = mergeRecentLogs(prior, Array.from({ length: 10000 }, (_, i) => line(i + 2)));
  assert.equal(result.length, RECENT_LOG_LIMIT);
  assert.equal(result[result.length - 1].id, 10001);
  assert.deepEqual(prior, [line(1)]);
});
