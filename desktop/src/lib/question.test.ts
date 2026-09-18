import assert from "node:assert/strict";
import test from "node:test";
import { buildAnswer, isStructured, sendsOnTap } from "./question.ts";
import type { PendingQuestion } from "../api/types";

const q = (over: Partial<PendingQuestion> = {}): PendingQuestion => ({
  id: 9,
  question: "Which cache backend?",
  kind: "choice",
  options: [{ label: "Redis" }, { label: "Memcached" }, { label: "LRU" }],
  allow_other: false,
  created_at: "2026-09-18T00:00:00Z",
  ...over,
});

test("only questions with options get the card", () => {
  assert.equal(isStructured(q()), true);
  assert.equal(isStructured(q({ kind: "confirm", options: [{ label: "Yes" }, { label: "No" }] })), true);
  assert.equal(isStructured(q({ kind: "text", options: [] })), false);
  assert.equal(isStructured(undefined), false);
});

test("a single choice sends on tap; multi_choice builds a selection first", () => {
  assert.equal(sendsOnTap(q()), true);
  assert.equal(sendsOnTap(q({ kind: "confirm" })), true);
  assert.equal(sendsOnTap(q({ kind: "multi_choice" })), false);
});

test("builds the request the endpoint expects", () => {
  assert.deepEqual(buildAnswer(q(), [2], ""), { answer: { question_id: 9, choices: [2] } });
  assert.deepEqual(buildAnswer(q({ kind: "multi_choice" }), [3, 1, 3], ""), {
    answer: { question_id: 9, choices: [1, 3] },
  });
  assert.deepEqual(buildAnswer(q({ allow_other: true }), [], "  Postgres  "), {
    answer: { question_id: 9, other: "Postgres" },
  });
  assert.deepEqual(buildAnswer(q({ kind: "multi_choice", allow_other: true }), [1], "and D"), {
    answer: { question_id: 9, choices: [1], other: "and D" },
  });
});

test("refuses what the server would", () => {
  assert.ok("error" in buildAnswer(q(), [], ""));
  assert.ok("error" in buildAnswer(q(), [1, 2], ""));
  assert.ok("error" in buildAnswer(q(), [], "Postgres"), "other without allow_other");
  assert.ok("error" in buildAnswer(q({ allow_other: true }), [1], "x"), "pick and other on a choice");
  assert.ok("error" in buildAnswer(q({ kind: "multi_choice" }), [7], ""), "out of range only");
});
