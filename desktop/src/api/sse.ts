import { apiBase } from "./client";
import type { LogLine } from "./types";

/// Subscribe to board-change notifications. The server pushes a snapshot on
/// every event_log change; we use it purely as a change signal and let the
/// store refetch the richer /api/tasks payload.
export function subscribeBoard(onChange: () => void): () => void {
  const source = new EventSource(`${apiBase()}/api/board/stream`);
  source.addEventListener("board", onChange);
  source.onerror = () => {
    // EventSource auto-reconnects; nothing to do.
  };
  return () => source.close();
}

/// Subscribe to a task's log stream starting after log id `since`.
export function subscribeTaskLogs(
  taskId: number,
  since: number,
  onLog: (log: LogLine) => void,
): () => void {
  const source = new EventSource(`${apiBase()}/api/tasks/${taskId}/stream?since=${since}`);
  source.addEventListener("log", (event) => {
    try {
      onLog(JSON.parse((event as MessageEvent).data));
    } catch {
      // skip malformed payloads
    }
  });
  return () => source.close();
}
