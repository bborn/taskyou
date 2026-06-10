import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import { subscribeTaskLogs } from "../api/sse";
import type { Dependencies, LogLine, Task } from "../api/types";
import { openExternal, openInEditor } from "../tauri";
import { store, useAppState } from "../store";
import { AttachmentsPanel } from "./AttachmentsPanel";
import { LogList } from "./LogList";
import { Markdown } from "./Markdown";
import { TerminalPane } from "./TerminalPane";

function AddBlockerInput({ taskId, onAdded }: { taskId: number; onAdded: () => void }) {
  const [value, setValue] = useState("");

  async function add() {
    const id = parseInt(value.replace("#", "").trim(), 10);
    if (!id) return;
    try {
      await api.addBlocker(taskId, id);
      setValue("");
      onAdded();
    } catch (e) {
      store.toast({
        title: "Failed to add dependency",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  return (
    <div className="dep" style={{ marginTop: 4 }}>
      <input
        value={value}
        placeholder="block on #id"
        style={{
          background: "var(--bg)",
          border: "1px solid var(--border)",
          borderRadius: 5,
          padding: "3px 8px",
          width: 110,
          outline: "none",
        }}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && void add()}
      />
    </div>
  );
}

export function DetailView({ taskId }: { taskId: number }) {
  const { tasks, executors } = useAppState();
  const [task, setTask] = useState<Task | null>(tasks.find((t) => t.id === taskId) ?? null);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [deps, setDeps] = useState<Dependencies | null>(null);
  const [showLogs, setShowLogs] = useState(false);

  // Keep the local task fresh when the store refreshes (status changes, etc.).
  const storeTask = tasks.find((t) => t.id === taskId);
  useEffect(() => {
    if (storeTask) setTask(storeTask);
  }, [storeTask]);

  const loadDetail = useCallback(async () => {
    try {
      const detail = await api.taskDetail(taskId);
      setTask(detail.task);
      setLogs(detail.logs);
    } catch (e) {
      store.toast({
        title: `Failed to load #${taskId}`,
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
    api.deps(taskId).then(setDeps).catch(() => setDeps(null));
  }, [taskId]);

  useEffect(() => {
    void loadDetail();
  }, [loadDetail]);

  // Live log stream (SSE), starting after the last loaded log.
  useEffect(() => {
    if (logs.length === 0 && !task) return undefined;
    const since = logs.length ? logs[logs.length - 1].id : 0;
    const unsubscribe = subscribeTaskLogs(taskId, since, (log) => {
      setLogs((prev) => (prev.some((l) => l.id === log.id) ? prev : [...prev, log]));
    });
    return unsubscribe;
    // Re-subscribe only per task; `since` is captured from the initial load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId, logs.length > 0]);

  if (!task) {
    return (
      <div className="detail">
        <div className="boot-screen">Loading task #{taskId}…</div>
      </div>
    );
  }

  const blocked = task.status === "blocked";

  return (
    <div className="detail">
      <div className="detail-header">
        <span className={`status-badge ${task.status}`}>{task.status}</span>
        <span className="card-id">#{task.id}</span>
        <span className="detail-title" title={task.title}>
          {task.title}
        </span>
        {task.pinned && <span className="badge pinned">📌</span>}
        {task.permission_mode && task.permission_mode !== "default" && (
          <span
            className={`badge ${task.permission_mode === "dangerous" ? "needs-input" : ""}`}
            title="Permission mode"
          >
            {task.permission_mode}
          </span>
        )}

        <div className="spacer" style={{ flex: 1 }} />

        <select
          value={task.executor || "claude"}
          title="Executor"
          onChange={async (e) => {
            await api.updateTask(task.id, { executor: e.target.value }).catch(() => {});
            void store.refreshTasks();
          }}
        >
          {(executors.length ? executors : [{ name: "claude", available: true, default: true }]).map(
            (ex) => (
              <option key={ex.name} value={ex.name} disabled={!ex.available}>
                {ex.name}
                {ex.available ? "" : " (not installed)"}
              </option>
            ),
          )}
        </select>

        {blocked ? (
          <button
            className="btn primary"
            onClick={() => store.setDialog({ kind: "retry", taskId: task.id })}
          >
            Reply
          </button>
        ) : (
          <button
            className="btn primary"
            disabled={task.status === "processing" || task.status === "queued"}
            onClick={() => void store.executeTask(task.id)}
          >
            Execute
          </button>
        )}
        <button className="btn" onClick={() => store.setForm({ kind: "edit", taskId: task.id })}>
          Edit
        </button>
        {task.worktree_path && (
          <button className="btn" title="Open worktree in editor (o)" onClick={() => void openInEditor(task.worktree_path!)}>
            Editor
          </button>
        )}
        {task.pr_url && (
          <button className="btn" title="Open PR (G)" onClick={() => void openExternal(task.pr_url)}>
            PR{task.pr_number ? ` #${task.pr_number}` : ""}
          </button>
        )}
        <button className="btn" title="More actions (S)" onClick={() => store.setDialog({ kind: "status", taskId: task.id })}>
          Status
        </button>
      </div>

      <div className="detail-body">
        <div className="detail-content" style={{ flex: "0 0 auto", maxHeight: "38%" }}>
          {task.body ? <Markdown source={task.body} /> : <span className="empty-hint">No description</span>}

          {task.summary && (
            <>
              <h3 className="section">Summary</h3>
              <Markdown source={task.summary} />
            </>
          )}

          <h3 className="section">Dependencies</h3>
          <div className="deps-list">
            {deps?.blockers?.map((d) => (
              <div key={`blocker-${d.id}`} className="dep">
                <span>🔒 blocked by</span>
                <a onClick={() => store.openDetail(d.id)}>
                  #{d.id} {d.title}
                </a>
                <span className={`status-badge ${d.status}`}>{d.status}</span>
                <button
                  className="icon-btn"
                  title="Remove dependency"
                  onClick={async () => {
                    await api.removeBlocker(task.id, d.id).catch(() => {});
                    api.deps(task.id).then(setDeps).catch(() => {});
                  }}
                >
                  ✕
                </button>
              </div>
            ))}
            {deps?.blocked_by?.map((d) => (
              <div key={`blocks-${d.id}`} className="dep">
                <span>⛓ blocks</span>
                <a onClick={() => store.openDetail(d.id)}>
                  #{d.id} {d.title}
                </a>
              </div>
            ))}
            {!deps?.blockers?.length && !deps?.blocked_by?.length && (
              <span className="empty-hint">No dependencies</span>
            )}
            <AddBlockerInput
              taskId={task.id}
              onAdded={() => api.deps(task.id).then(setDeps).catch(() => {})}
            />
          </div>

          <h3 className="section">Attachments</h3>
          <AttachmentsPanel taskId={task.id} />

          <h3 className="section" style={{ cursor: "pointer" }} onClick={() => setShowLogs(!showLogs)}>
            Execution log {showLogs ? "▾" : "▸"} <span style={{ fontWeight: 400 }}>({logs.length})</span>
          </h3>
          {showLogs && <LogList logs={logs} />}
        </div>

        <TerminalPane task={task} />
      </div>
    </div>
  );
}
