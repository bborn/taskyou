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
  onLogs: (logs: LogLine[]) => void,
): () => void {
  const source = new EventSource(`${apiBase()}/api/tasks/${taskId}/stream?since=${since}`);
  const pending = new Map<number, LogLine>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  source.addEventListener("log", (event) => {
    try {
      const line = JSON.parse((event as MessageEvent).data) as LogLine;
      pending.set(line.id, line);
      // Background tabs throttle timers. Bound pending storage as well as state.
      if (pending.size > 500) pending.delete(pending.keys().next().value!);
      if (timer === undefined) timer = setTimeout(() => {
        timer = undefined;
        const batch = [...pending.values()];
        pending.clear();
        onLogs(batch);
      }, 33);
    } catch {
      // skip malformed payloads
    }
  });
  return () => {
    source.close();
    clearTimeout(timer);
    pending.clear();
  };
}
