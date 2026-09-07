import test from "node:test";
import assert from "node:assert/strict";
import { CoalescedRefresh } from "./refresh.ts";

test("overlapping updates become one ordered follow-up", async () => {
  let release!: () => void;
  let calls = 0;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const queue = new CoalescedRefresh(async () => { if (++calls === 1) await gate; });
  const first = queue.request();
  await Promise.resolve();
  for (let i = 0; i < 20; i++) assert.equal(queue.request(), first);
  assert.equal(calls, 1);
  release();
  await first;
  assert.equal(calls, 2);
});

test("a failed refresh releases the queue for retry", async () => {
  let calls = 0;
  const queue = new CoalescedRefresh(async () => { if (++calls === 1) throw new Error("offline"); });
  await assert.rejects(queue.request(), /offline/);
  await queue.request();
  assert.equal(calls, 2);
});
