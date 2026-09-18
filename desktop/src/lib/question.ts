import type { PendingQuestion, QuestionAnswer } from "../api/types";

/** A question with answers to pick, as opposed to a plain one answered in the
 * person's own words (which the reply box already handles). */
export function isStructured(q: PendingQuestion | undefined | null): q is PendingQuestion {
  return !!q && q.kind !== "text" && q.options.length > 0;
}

/** Whether tapping an option sends it straight away. Only multi_choice needs a
 * separate Send: there the taps build a selection. */
export function sendsOnTap(q: PendingQuestion): boolean {
  return q.kind !== "multi_choice";
}

/**
 * Builds the answer request from what the person picked (1-based option
 * numbers) and typed, or returns the reason it cannot be sent yet. The server
 * checks again; this only keeps the Send button honest.
 */
export function buildAnswer(
  q: PendingQuestion,
  picked: readonly number[],
  other: string,
): { answer: QuestionAnswer } | { error: string } {
  const text = other.trim();
  const choices = [...new Set(picked)].filter((n) => n >= 1 && n <= q.options.length).sort((a, b) => a - b);
  if (text && !q.allow_other) return { error: "This question only takes one of its options." };
  if (choices.length === 0 && !text) {
    return { error: q.kind === "multi_choice" ? "Pick at least one option." : "Pick an option." };
  }
  if (q.kind !== "multi_choice") {
    if (choices.length > 1) return { error: "Pick one option." };
    if (choices.length === 1 && text) return { error: "Pick an option or answer in your own words, not both." };
  }
  const answer: QuestionAnswer = { question_id: q.id };
  if (choices.length) answer.choices = choices;
  if (text) answer.other = text;
  return { answer };
}
