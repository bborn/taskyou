import type { LogLine } from "../api/types";

export const RECENT_LOG_LIMIT = 500;

// One merge per batch, bounded regardless of how long the detail stays open.
export function mergeRecentLogs(current: LogLine[], incoming: LogLine[]): LogLine[] {
  const byId = new Map(current.map((line) => [line.id, line]));
  for (const line of incoming) byId.set(line.id, line);
  return [...byId.values()].sort((a, b) => a.id - b.id).slice(-RECENT_LOG_LIMIT);
}
