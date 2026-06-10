import { useEffect, useRef, useState } from "react";
import { store, useAppState } from "../store";

export function Dialogs() {
  const { dialog } = useAppState();
  if (!dialog) return null;

  switch (dialog.kind) {
    case "confirm":
      return <ConfirmDialog {...dialog} />;
    case "retry":
      return <RetryDialog taskId={dialog.taskId} />;
    case "status":
      return <StatusDialog taskId={dialog.taskId} />;
    case "help":
      return <HelpDialog />;
  }
}

function ConfirmDialog({
  title,
  message,
  danger,
  onConfirm,
}: {
  title: string;
  message: string;
  danger?: boolean;
  onConfirm: () => void;
}) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  useEffect(() => confirmRef.current?.focus(), []);

  function confirm() {
    store.setDialog(null);
    onConfirm();
  }

  return (
    <div className="overlay" onMouseDown={() => store.setDialog(null)}>
      <div className="modal" onMouseDown={(e) => e.stopPropagation()}>
        <div className="modal-header">{title}</div>
        <div className="modal-body">{message}</div>
        <div className="modal-footer">
          <button className="btn" onClick={() => store.setDialog(null)}>
            Cancel
          </button>
          <button
            ref={confirmRef}
            className={`btn ${danger ? "danger" : "primary"}`}
            onClick={confirm}
            onKeyDown={(e) => e.key === "Enter" && confirm()}
          >
            Confirm
          </button>
        </div>
      </div>
    </div>
  );
}

function RetryDialog({ taskId }: { taskId: number }) {
  const { tasks } = useAppState();
  const task = tasks.find((t) => t.id === taskId);
  const [feedback, setFeedback] = useState("");
  const [dangerous, setDangerous] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  useEffect(() => textareaRef.current?.focus(), []);

  async function submit() {
    store.setDialog(null);
    if (dangerous) {
      const { api } = await import("../api/client");
      await api.updateTask(taskId, { permission_mode: "dangerous" }).catch(() => {});
    }
    void store.retryTask(taskId, feedback);
  }

  return (
    <div className="overlay" onMouseDown={() => store.setDialog(null)}>
      <div className="modal" onMouseDown={(e) => e.stopPropagation()}>
        <div className="modal-header">
          Reply to #{taskId} {task ? `— ${task.title}` : ""}
        </div>
        <div className="modal-body">
          <div className="form-row">
            <label>Feedback / answer</label>
            <textarea
              ref={textareaRef}
              rows={5}
              value={feedback}
              placeholder="Answer the executor's question or give new instructions…"
              onChange={(e) => setFeedback(e.target.value)}
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") void submit();
              }}
            />
            <span className="hint">⌘↩ to send</span>
          </div>
          <label style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 12 }}>
            <input
              type="checkbox"
              checked={dangerous}
              onChange={(e) => setDangerous(e.target.checked)}
            />
            Resume in dangerous mode (skip permission prompts)
          </label>
        </div>
        <div className="modal-footer">
          <button className="btn" onClick={() => store.setDialog(null)}>
            Cancel
          </button>
          <button className="btn primary" onClick={() => void submit()}>
            Send & resume
          </button>
        </div>
      </div>
    </div>
  );
}

const STATUSES = ["backlog", "queued", "processing", "blocked", "done", "archived"];

function StatusDialog({ taskId }: { taskId: number }) {
  const { tasks } = useAppState();
  const task = tasks.find((t) => t.id === taskId);
  const [status, setStatus] = useState<string>(task?.status ?? "backlog");

  return (
    <div className="overlay" onMouseDown={() => store.setDialog(null)}>
      <div className="modal" onMouseDown={(e) => e.stopPropagation()}>
        <div className="modal-header">Change status of #{taskId}</div>
        <div className="modal-body">
          <div className="form-row">
            <label>Status</label>
            <select value={status} onChange={(e) => setStatus(e.target.value)} autoFocus>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="modal-footer">
          <button className="btn" onClick={() => store.setDialog(null)}>
            Cancel
          </button>
          <button
            className="btn primary"
            onClick={() => {
              store.setDialog(null);
              void store.setTaskStatus(taskId, status);
            }}
          >
            Apply
          </button>
        </div>
      </div>
    </div>
  );
}

const HELP: [string, string][] = [
  ["↑↓←→", "Navigate board"],
  ["Enter", "Open task"],
  ["n", "New task"],
  ["e", "Edit task"],
  ["x / X", "Execute / execute dangerously"],
  ["r", "Reply to blocked task"],
  ["c", "Close task"],
  ["a / d", "Archive / delete task"],
  ["t", "Pin task"],
  ["S", "Change status"],
  ["/", "Filter board"],
  ["p or f / ⌘P", "Search tasks"],
  ["o", "Open worktree in editor"],
  ["b / G", "Open branch / PR"],
  ["[ / ]", "Collapse Backlog / Done"],
  ["B P L D", "Jump to column"],
  ["g", "Go to last notification"],
  ["!", "Cycle permission mode"],
  ["s", "Settings"],
  ["R", "Refresh"],
  ["Esc", "Back / close"],
];

function HelpDialog() {
  return (
    <div className="overlay" onMouseDown={() => store.setDialog(null)}>
      <div className="modal" onMouseDown={(e) => e.stopPropagation()}>
        <div className="modal-header">Keyboard shortcuts</div>
        <div className="modal-body">
          <div className="help-grid">
            {HELP.map(([key, desc]) => (
              <KeyRow key={key} k={key} desc={desc} />
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function KeyRow({ k, desc }: { k: string; desc: string }) {
  return (
    <>
      <span className="kbd">{k}</span>
      <span>{desc}</span>
    </>
  );
}
