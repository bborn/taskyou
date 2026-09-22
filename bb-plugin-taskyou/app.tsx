import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import {
  definePluginApp,
  useRealtime,
  useRealtimeConnectionState,
  useRpc,
  useSettings,
} from "@get-bb/plugin-sdk/app";
import type {
  rpcContract,
  Task,
  Project,
  TaskType,
  ExecutorInfo,
} from "./server";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const controlClass =
  "w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50";
const columns = [
  { title: "Backlog", statuses: ["backlog"] },
  { title: "In progress", statuses: ["queued", "processing"] },
  { title: "Blocked", statuses: ["blocked"] },
  { title: "Done", statuses: ["done"] },
];
const statusLabels: Record<string, string> = {
  backlog: "Backlog",
  queued: "Queued",
  processing: "Processing",
  blocked: "Blocked",
  done: "Done",
  archived: "Archived",
};
const message = (cause: unknown) =>
  cause instanceof Error ? cause.message : String(cause);

/** Signals are hints: reconcile on reconnect and periodically. Coalesce bursts
 * and allow only one request plus one queued refresh per mounted view. */
function useLiveResource<T>(load: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const { values } = useSettings();
  const settingsKey = JSON.stringify(values);
  const connection = useRealtimeConnectionState();
  const previousConnection = useRef(connection);
  const refreshRef = useRef<() => void>(() => {});
  const refresh = useCallback(() => refreshRef.current(), []);
  useEffect(() => {
    let disposed = false;
    let running = false;
    let queued = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const fetchNow = async () => {
      if (disposed) return;
      if (running) {
        queued = true;
        return;
      }
      running = true;
      setLoading(true);
      try {
        const next = await load();
        if (!disposed) {
          setData(next);
          setError(null);
        }
      } catch (cause) {
        if (!disposed) setError(message(cause));
      } finally {
        running = false;
        if (!disposed) {
          setLoading(false);
          if (queued) {
            queued = false;
            schedule();
          }
        }
      }
    };
    const schedule = () => {
      if (disposed || timer !== undefined) return;
      timer = setTimeout(() => {
        timer = undefined;
        void fetchNow();
      }, 200);
    };
    refreshRef.current = schedule;
    setData(null);
    setError(null);
    void fetchNow();
    const interval = setInterval(schedule, 30_000);
    return () => {
      disposed = true;
      clearTimeout(timer);
      clearInterval(interval);
      refreshRef.current = () => {};
    };
  }, [load, settingsKey]);
  useRealtime("taskyou-changed", refresh);
  useEffect(() => {
    if (
      connection === "connected" &&
      previousConnection.current !== "connected"
    )
      refresh();
    previousConnection.current = connection;
  }, [connection, refresh]);
  return { data, error, loading, refresh, connection };
}

function EmptyState({ children }: { children: ReactNode }) {
  return (
    <p
      role="status"
      className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground"
    >
      {children}
    </p>
  );
}

function ConnectionError({
  error,
  refresh,
}: {
  error: string;
  refresh: () => void;
}) {
  return (
    <div
      role="alert"
      className="space-y-2 rounded-lg border border-destructive/30 bg-card p-4 text-sm"
    >
      <p className="font-medium text-destructive">Could not load TaskYou</p>
      <p className="break-words">{error}</p>
      <p className="text-muted-foreground">
        Start <code>ty serve</code> and check the API URL in the TaskYou plugin
        settings. The URL must be reachable from the bb server.
      </p>
      <Button variant="outline" size="sm" onClick={refresh}>
        Try again
      </Button>
    </div>
  );
}

/** A refresh failed but a previous load is still shown, so the board is stale
 * rather than empty. Distinct from {@link ConnectionError}, which is only
 * coherent when there is no data to display. */
function StaleDataBanner({
  error,
  refresh,
}: {
  error: string;
  refresh: () => void;
}) {
  return (
    <div
      role="alert"
      className="space-y-2 rounded-lg border border-border bg-card p-4 text-sm"
    >
      <p className="font-medium">Last refresh failed</p>
      <p className="break-words">{error}</p>
      <p className="text-muted-foreground">
        Showing data from the previous load while waiting to retry.
      </p>
      <Button variant="outline" size="sm" onClick={refresh}>
        Try again
      </Button>
    </div>
  );
}

type Catalog = {
  projects: Project[];
  types: TaskType[];
  executors: ExecutorInfo[];
};
type Modal =
  | { kind: "new" }
  | { kind: "detail"; id: number }
  | { kind: "edit"; task: Task };

function TaskEditor({
  task,
  catalog,
  onSaved,
  onDismiss,
}: {
  task?: Task;
  catalog: Catalog;
  onSaved: () => void;
  onDismiss: () => void;
}) {
  const rpc = useRpc<typeof rpcContract>();
  const [title, setTitle] = useState(task?.title ?? "");
  const [body, setBody] = useState(task?.body ?? "");
  const [project, setProject] = useState(task?.project ?? "");
  const [type, setType] = useState(task?.type ?? "");
  const [executor, setExecutor] = useState(task?.executor ?? "");
  const [execute, setExecute] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!title.trim() || pending) return;
    setPending(true);
    setError(null);
    try {
      const fields = { title: title.trim(), body, project, type, executor };
      if (task) await rpc.call("update", { id: task.id, ...fields });
      else await rpc.call("create", { ...fields, execute });
      if (active.current) onSaved();
    } catch (cause) {
      if (active.current) setError(message(cause));
    } finally {
      if (active.current) setPending(false);
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onDismiss();
      }}
    >
      <DialogContent
        hideCloseButton={pending}
        className="max-h-[90dvh] overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle>
            {task ? `Edit task #${task.id}` : "New task"}
          </DialogTitle>
          <DialogDescription>
            {task
              ? "Update this task in TaskYou."
              : "Capture a task in your backlog, or queue it for execution."}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <fieldset disabled={pending} className="space-y-4">
            <label className="block space-y-1 text-sm">
              Title
              <Input
                autoFocus
                required
                maxLength={2000}
                value={title}
                onChange={(e) => setTitle(e.target.value)}
              />
            </label>
            <label className="block space-y-1 text-sm">
              Description
              <textarea
                className={controlClass}
                rows={5}
                value={body}
                onChange={(e) => setBody(e.target.value)}
              />
            </label>
            <label className="block space-y-1 text-sm">
              Project
              <select
                className={controlClass}
                value={project}
                onChange={(e) => setProject(e.target.value)}
              >
                <option value="">No project</option>
                {project &&
                  !catalog.projects.some((p) => p.name === project) && (
                    <option value={project}>{project}</option>
                  )}
                {catalog.projects.map((p) => (
                  <option key={p.id} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <div className="grid grid-cols-2 gap-3">
              <label className="block space-y-1 text-sm">
                Task type
                <select
                  className={controlClass}
                  value={type}
                  onChange={(e) => setType(e.target.value)}
                >
                  <option value="" disabled={!!task?.type}>
                    Default type
                  </option>
                  {type && !catalog.types.some((t) => t.name === type) && (
                    <option value={type}>{type}</option>
                  )}
                  {catalog.types.map((t) => (
                    <option key={t.id} value={t.name}>
                      {t.label || t.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="block space-y-1 text-sm">
                Executor
                <select
                  className={controlClass}
                  value={executor}
                  onChange={(e) => setExecutor(e.target.value)}
                >
                  <option value="">TaskYou default</option>
                  {executor &&
                    !catalog.executors.some((e) => e.name === executor) && (
                      <option value={executor}>{executor}</option>
                    )}
                  {catalog.executors.map((e) => (
                    <option key={e.name} value={e.name}>
                      {e.name}
                      {!e.available ? " (unavailable)" : ""}
                      {e.default ? " (default)" : ""}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            {!task && (
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={execute}
                  onChange={(e) => setExecute(e.target.checked)}
                />
                Queue for execution after saving
              </label>
            )}
          </fieldset>
          {error && (
            <p role="alert" className="break-words text-sm text-destructive">
              {error}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={onDismiss}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={pending || !title.trim()}>
              {pending
                ? "Saving…"
                : task
                  ? "Save changes"
                  : execute
                    ? "Create and queue"
                    : "Create task"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TaskDetail({
  id,
  onDismiss,
  onEdit,
  onChanged,
}: {
  id: number;
  onDismiss: () => void;
  onEdit: (task: Task) => void;
  onChanged: () => void;
}) {
  const rpc = useRpc<typeof rpcContract>();
  const load = useCallback(() => rpc.call("detail", { id }), [rpc, id]);
  const { data, error, refresh } = useLiveResource(load);
  const [pending, setPending] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState("");
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const run = async (action: "execute" | "retry" | "close") => {
    if (pending) return;
    setPending(true);
    setActionError(null);
    try {
      if (action === "retry") await rpc.call("retry", { id, feedback });
      else await rpc.call(action, { id });
      if (active.current) {
        setFeedback("");
        refresh();
        onChanged();
      }
    } catch (cause) {
      if (active.current) setActionError(message(cause));
    } finally {
      if (active.current) setPending(false);
    }
  };
  const task = data?.task;
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onDismiss();
      }}
    >
      <DialogContent
        hideCloseButton={pending}
        className="max-h-[90dvh] max-w-2xl overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle>{task?.title ?? `Task #${id}`}</DialogTitle>
          <DialogDescription>
            #{id}
            {task
              ? ` · ${statusLabels[task.status] ?? task.status} · ${task.project || "No project"} · ${task.executor || "Default executor"}`
              : " · Loading details"}
          </DialogDescription>
        </DialogHeader>
        {error && !data && <ConnectionError error={error} refresh={refresh} />}
        {error && data && <StaleDataBanner error={error} refresh={refresh} />}
        {!data && !error && <EmptyState>Loading task…</EmptyState>}
        {task && data && (
          <>
            <p className="whitespace-pre-wrap break-words text-sm">
              {task.body || "No description."}
            </p>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                disabled={pending}
                onClick={() => onEdit(task)}
              >
                Edit task
              </Button>
              {task.status === "backlog" && (
                <Button disabled={pending} onClick={() => void run("execute")}>
                  Execute
                </Button>
              )}
              {task.status !== "done" && task.status !== "archived" && (
                <Button
                  variant="outline"
                  disabled={pending}
                  onClick={() => void run("close")}
                >
                  Close task
                </Button>
              )}
            </div>
            {(task.status === "blocked" || task.status === "done") && (
              <form
                className="space-y-2 rounded-lg border border-border p-3"
                onSubmit={(e) => {
                  e.preventDefault();
                  void run("retry");
                }}
              >
                <label className="block space-y-1 text-sm">
                  Retry feedback
                  <textarea
                    className={controlClass}
                    rows={3}
                    value={feedback}
                    disabled={pending}
                    placeholder="Instructions for the next attempt (optional)"
                    onChange={(e) => setFeedback(e.target.value)}
                  />
                </label>
                <Button type="submit" disabled={pending}>
                  Retry task
                </Button>
              </form>
            )}
            {pending && (
              <p role="status" className="text-sm text-muted-foreground">
                Updating task…
              </p>
            )}
            {actionError && (
              <p role="alert" className="text-sm text-destructive">
                {actionError}
              </p>
            )}
            <section aria-label="Task logs" className="space-y-2">
              <h3 className="text-sm font-medium">Recent logs</h3>
              {data.logs.length === 0 ? (
                <p className="text-sm text-muted-foreground">No logs yet.</p>
              ) : (
                <ol className="max-h-64 space-y-2 overflow-auto rounded-md border border-border bg-muted/30 p-3 font-mono text-xs">
                  {data.logs.map((line) => (
                    <li key={line.id}>
                      <span className="text-muted-foreground">
                        {line.line_type}{" "}
                      </span>
                      <span className="whitespace-pre-wrap break-words">
                        {line.content}
                      </span>
                    </li>
                  ))}
                </ol>
              )}
            </section>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Board() {
  const rpc = useRpc<typeof rpcContract>();
  const { values } = useSettings();
  const settingsKey = JSON.stringify(values);
  const load = useCallback(() => rpc.call("board", null), [rpc]);
  const { data, error, loading, refresh, connection } = useLiveResource(load);
  const [query, setQuery] = useState("");
  const [project, setProject] = useState("");
  const [modal, setModal] = useState<Modal | null>(null);
  // An id from one endpoint must never be edited against another endpoint.
  useEffect(() => {
    setModal(null);
    setProject("");
  }, [settingsKey]);
  useRealtime("taskyou-changed", (payload) => {
    if (
      payload &&
      typeof payload === "object" &&
      "reason" in payload &&
      payload.reason === "settings"
    ) {
      setModal(null);
      setProject("");
    }
  });
  const returnFocus = useRef<HTMLElement | null>(null);
  const openModal = (next: Modal) => {
    returnFocus.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    setModal(next);
  };
  const dismiss = () => {
    setModal(null);
    queueMicrotask(() => returnFocus.current?.focus());
  };
  const saved = () => {
    dismiss();
    refresh();
  };
  const matches =
    data?.tasks.filter(
      (task) =>
        (!project || task.project === project) &&
        `${task.id} ${task.title} ${task.body} ${task.project}`
          .toLocaleLowerCase()
          .includes(query.trim().toLocaleLowerCase()),
    ) ?? [];
  return (
    <div className="h-full min-h-0 flex-1 overflow-auto">
      <div className="mx-auto flex min-h-full w-full max-w-[1600px] flex-col gap-4 p-4 md:p-5">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            className="min-w-40 flex-1"
            aria-label="Search tasks"
            placeholder="Search tasks…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <select
            className={`${controlClass} w-auto max-w-52`}
            aria-label="Filter by project"
            value={project}
            onChange={(e) => setProject(e.target.value)}
          >
            <option value="">All projects</option>
            {data?.projects.map((p) => (
              <option key={p.id} value={p.name}>
                {p.name}
              </option>
            ))}
          </select>
          <Button variant="outline" disabled={loading} onClick={refresh}>
            Refresh
          </Button>
          <Button disabled={!data} onClick={() => openModal({ kind: "new" })}>
            New task
          </Button>
        </div>
        {connection !== "connected" && (
          <p role="status" className="text-xs text-muted-foreground">
            Reconnecting to bb… The board will refresh when connected.
          </p>
        )}
        {error && !data && <ConnectionError error={error} refresh={refresh} />}
        {error && data && <StaleDataBanner error={error} refresh={refresh} />}
        {!data && !error && <EmptyState>Loading tasks…</EmptyState>}
        {data && (
          <>
            {data.truncated && (
              <p role="status" className="text-sm text-muted-foreground">
                Showing up to 200 tasks per status. Search and project filters
                apply to these loaded tasks.
              </p>
            )}
            <div className="grid flex-1 grid-cols-1 items-start gap-3 sm:grid-cols-2 xl:grid-cols-4">
              {columns.map((column) => {
                const tasks = matches.filter((task) =>
                  column.statuses.includes(task.status),
                );
                return (
                  <section
                    aria-label={column.title}
                    key={column.title}
                    className="min-w-0 rounded-xl border border-border bg-muted/20 p-3"
                  >
                    <div className="mb-3 flex items-center justify-between gap-2">
                      <h2 className="text-sm font-medium">{column.title}</h2>
                      <span className="rounded-md bg-muted px-2 py-0.5 text-xs tabular-nums text-muted-foreground">
                        {tasks.length}
                      </span>
                    </div>
                    {tasks.length === 0 ? (
                      <p className="py-6 text-center text-xs text-muted-foreground">
                        {query || project ? "No matching tasks" : "No tasks"}
                      </p>
                    ) : (
                      <ul className="space-y-2">
                        {tasks.map((task) => (
                          <li key={task.id}>
                            <button
                              type="button"
                              className="w-full space-y-3 rounded-lg border border-border bg-card p-3 text-left shadow-sm transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                              onClick={() =>
                                openModal({ kind: "detail", id: task.id })
                              }
                            >
                              <div className="flex items-start gap-2">
                                <span className="flex-1 break-words text-sm font-medium">
                                  {task.title}
                                </span>
                                <span className="text-xs tabular-nums text-muted-foreground">
                                  #{task.id}
                                </span>
                              </div>
                              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                                <span className="rounded border border-border px-1.5 py-0.5">
                                  {statusLabels[task.status] ?? task.status}
                                </span>
                                <span>{task.project || "No project"}</span>
                                {task.executor && (
                                  <span>· {task.executor}</span>
                                )}
                              </div>
                            </button>
                          </li>
                        ))}
                      </ul>
                    )}
                  </section>
                );
              })}
            </div>
            <p className="text-xs text-muted-foreground">
              {matches.length} {matches.length === 1 ? "task" : "tasks"}
              {loading ? " · Refreshing…" : " · Managed by TaskYou"}
            </p>
          </>
        )}
      </div>
      {modal?.kind === "detail" && (
        <TaskDetail
          key={modal.id}
          id={modal.id}
          onDismiss={dismiss}
          onChanged={refresh}
          onEdit={(task) => setModal({ kind: "edit", task })}
        />
      )}
      {data && modal && modal.kind !== "detail" && (
        <TaskEditor
          key={modal.kind === "edit" ? modal.task.id : "new"}
          task={modal.kind === "edit" ? modal.task : undefined}
          catalog={data}
          onSaved={saved}
          onDismiss={dismiss}
        />
      )}
    </div>
  );
}

export default definePluginApp((app) => {
  app.slots.navPanel({
    id: "board",
    title: "TaskYou",
    icon: "ListTodo",
    path: "board",
    component: Board,
  });
});
