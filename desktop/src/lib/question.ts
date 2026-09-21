import type { LogLine } from "../api/types";

/** One answer an agent offered with taskyou_needs_input. Picking it sends the
 * label to the agent as the reply. */
export interface QuestionOption {
  label: string;
  description?: string;
}

/** Line type of the offered answers: a JSON array written just before the
 * question's own "question" line (db.LogQuestionOptions). */
export const QUESTION_OPTIONS = "question_options";

export function parseOptions(content: string): QuestionOption[] | null {
  try {
    const value: unknown = JSON.parse(content);
    if (!Array.isArray(value)) return null;
    const opts = value.filter(
      (o): o is QuestionOption => !!o && typeof o.label === "string" && o.label.trim() !== "",
    );
    return opts.length ? opts : null;
  } catch {
    return null;
  }
}

/** The options offered with the question at logs[i] (logs in chronological
 * order), or null when it offered none. */
export function optionsFor(logs: readonly LogLine[], i: number): QuestionOption[] | null {
  const prev = logs[i - 1];
  if (!prev || prev.line_type !== QUESTION_OPTIONS || prev.id > logs[i].id) return null;
  return parseOptions(prev.content);
}

const RESUMED = new Set(["Agent resumed working", "Claude resumed working"]);

/** Whether a line logged after a question shows the agent has moved past it:
 * it resumed (a prompt reached it), someone replied, or it went back to work.
 * The one tool line that does not count is the taskyou_needs_input call that
 * asked the question. */
function movedOn(log: LogLine): boolean {
  switch (log.line_type) {
    case "system":
      return RESUMED.has(log.content);
    case "text":
    case "user":
      return true;
    case "tool":
      return !log.content.includes("taskyou_needs_input");
    default:
      return false;
  }
}

/**
 * The question a blocked task is waiting on, when its agent offered answers to
 * tap. Null when the task is not blocked, the newest question offered no
 * options, or the agent has moved past it since.
 */
export function pendingQuestion(
  status: string,
  logs: readonly LogLine[],
): { id: number; question: string; options: QuestionOption[] } | null {
  if (status !== "blocked") return null;
  for (let i = logs.length - 1; i >= 0; i--) {
    const log = logs[i];
    if (log.line_type !== "question") {
      if (movedOn(log)) return null;
      continue;
    }
    const options = optionsFor(logs, i);
    return options ? { id: log.id, question: log.content, options } : null;
  }
  return null;
}
