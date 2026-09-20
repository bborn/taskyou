import assert from "node:assert/strict";
import test from "node:test";
import { optionsFor, parseOptions, pendingQuestion } from "./question.ts";

let nextId = 1;
const line = (line_type: string, content: string) => ({
  id: nextId++,
  line_type,
  content,
  created_at: "2026-09-18T00:00:00Z",
});
const offered = JSON.stringify([{ label: "Redis", description: "already in prod" }, { label: "Memcached" }]);

// What taskyou_needs_input with options leaves behind: the options, the
// question, then the hook's line for the tool call itself.
const asked = () => [
  line("tool", "Bash: go test ./..."),
  line("question_options", offered),
  line("question", "Which cache backend?"),
  line("tool", "mcp__taskyou__taskyou_needs_input"),
  line("system", "Waiting for user input"),
];

test("a blocked task shows the options offered with its newest question", () => {
  const got = pendingQuestion("blocked", asked());
  assert.equal(got?.question, "Which cache backend?");
  assert.deepEqual(got?.options.map((o) => o.label), ["Redis", "Memcached"]);
});

test("nothing to tap unless the task is blocked", () => {
  assert.equal(pendingQuestion("processing", asked()), null);
});

test("a plain question offers nothing, even after an earlier one did", () => {
  const logs = [...asked(), line("system", "Agent resumed working"), line("question", "What next?")];
  assert.equal(pendingQuestion("blocked", logs), null);
});

test("options go stale once the agent moves past the question", () => {
  for (const after of [
    line("system", "Agent resumed working"),
    line("text", "Feedback: use Redis"),
    line("user", "Replied: Redis"),
    line("tool", "Edit: cache.go"),
  ]) {
    assert.equal(pendingQuestion("blocked", [...asked(), after]), null, `after ${after.line_type}: ${after.content}`);
  }
});

test("parses what the MCP tool writes and rejects anything else", () => {
  assert.deepEqual(parseOptions(offered)?.map((o) => o.label), ["Redis", "Memcached"]);
  assert.equal(parseOptions("not json"), null);
  assert.equal(parseOptions('{"label":"x"}'), null);
  assert.equal(parseOptions('[{"description":"no label"}]'), null);
});

test("options belong to the question written right after them", () => {
  const logs = asked();
  assert.ok(optionsFor(logs, 2));
  assert.equal(optionsFor(logs, 0), null);
});
