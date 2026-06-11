import { useCallback, useEffect, useState } from "react";
import { ChevronDown, ChevronRight, GitPullRequest, Pin, Code2 } from "lucide-react";
import { api } from "../api/client";
import { subscribeTaskLogs } from "../api/sse";
import type { Dependencies, LogLine, Task } from "../api/types";
import { openExternal, openInEditor } from "../tauri";
import { store, useAppState } from "../store";
import { AttachmentsPanel } from "./AttachmentsPanel";
import { LogList } from "./LogList";
import { Markdown } from "./Markdown";
import { TerminalPane } from "./TerminalPane";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const STATUS_BADGE: Record<string, string> = {
  backlog: "border-status-backlog/50 text-status-backlog",
  queued: "border-amber-300/50 text-amber-300",
  processing: "border-status-processing/50 text-status-processing",
  blocked: "border-status-blocked/50 text-status-blocked",
  done: "text-muted-foreground",
  archived: "text-muted-foreground",
};

function SectionTitle({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) {
  return (
    <h3
      className={`mb-1.5 mt-4 flex items-center gap-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground ${
        onClick ? "hover:text-foreground" : ""
      }`}
      onClick={onClick}
    >
      {children}
    </h3>
  );
}

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
    <Input
      className="mt-1 h-6 w-28 text-xs"
      value={value}
      placeholder="block on #id"
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => e.key === "Enter" && void add()}
    />
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
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        Loading task #{taskId}…
      </div>
    );
  }

  const blocked = task.status === "blocked";
  const refreshDeps = () => api.deps(task.id).then(setDeps).catch(() => {});

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-surface-1 px-4 py-2.5">
        <Badge variant="outline" className={STATUS_BADGE[task.status] ?? ""}>
          {task.status}
        </Badge>
        <span className="font-mono text-[11px] text-muted-foreground">#{task.id}</span>
        <span className="max-w-[44ch] truncate text-sm font-semibold" title={task.title}>
          {task.title}
        </span>
        {task.pinned && <Pin className="size-3.5 text-amber-300" />}
        {task.permission_mode && task.permission_mode !== "default" && (
          <Badge
            variant={task.permission_mode === "dangerous" ? "destructive" : "outline"}
            title="Permission mode"
          >
            {task.permission_mode}
          </Badge>
        )}

        <div className="flex-1" />

        <Select
          value={task.executor || "claude"}
          onValueChange={async (v) => {
            await api.updateTask(task.id, { executor: v }).catch(() => {});
            void store.refreshTasks();
          }}
        >
          <SelectTrigger size="sm" className="w-32" title="Executor">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(executors.length ? executors : [{ name: "claude", available: true, default: true }]).map(
              (ex) => (
                <SelectItem key={ex.name} value={ex.name} disabled={!ex.available}>
                  {ex.name}
                  {ex.available ? "" : " (not installed)"}
                </SelectItem>
              ),
            )}
          </SelectContent>
        </Select>

        {blocked ? (
          <Button size="sm" onClick={() => store.setDialog({ kind: "retry", taskId: task.id })}>
            Reply
          </Button>
        ) : (
          <Button
            size="sm"
            disabled={task.status === "processing" || task.status === "queued"}
            onClick={() => void store.executeTask(task.id)}
          >
            Execute
          </Button>
        )}
        <Button variant="outline" size="sm" onClick={() => store.setForm({ kind: "edit", taskId: task.id })}>
          Edit
        </Button>
        {task.worktree_path && (
          <Button
            variant="outline"
            size="sm"
            title="Open worktree in editor (o)"
            onClick={() => void openInEditor(task.worktree_path!)}
          >
            <Code2 className="size-3.5" /> Editor
          </Button>
        )}
        {task.pr_url && (
          <Button
            variant="outline"
            size="sm"
            title="Open PR (G)"
            onClick={() => void openExternal(task.pr_url)}
          >
            <GitPullRequest className="size-3.5" />
            {task.pr_number ? `#${task.pr_number}` : "PR"}
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          title="Change status (S)"
          onClick={() => store.setDialog({ kind: "status", taskId: task.id })}
        >
          Status
        </Button>
      </div>

      <div className="flex min-h-0 flex-1 flex-col">
        <div className="max-h-[38%] shrink-0 overflow-y-auto border-b px-5 py-3.5 select-text">
          {task.body ? (
            <Markdown source={task.body} />
          ) : (
            <span className="text-xs text-muted-foreground">No description</span>
          )}

          {task.summary && (
            <>
              <SectionTitle>Summary</SectionTitle>
              <Markdown source={task.summary} />
            </>
          )}

          <SectionTitle>Dependencies</SectionTitle>
          <div className="flex flex-col gap-1 text-[12.5px]">
            {deps?.blockers?.map((d) => (
              <div key={`blocker-${d.id}`} className="flex items-center gap-2">
                <span className="text-muted-foreground">🔒 blocked by</span>
                <a onClick={() => store.openDetail(d.id)} className="text-status-backlog">
                  #{d.id} {d.title}
                </a>
                <Badge variant="outline" className={`h-4.5 px-1.5 text-[10px] ${STATUS_BADGE[d.status] ?? ""}`}>
                  {d.status}
                </Badge>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-5"
                  title="Remove dependency"
                  onClick={async () => {
                    await api.removeBlocker(task.id, d.id).catch(() => {});
                    refreshDeps();
                  }}
                >
                  ✕
                </Button>
              </div>
            ))}
            {deps?.blocked_by?.map((d) => (
              <div key={`blocks-${d.id}`} className="flex items-center gap-2">
                <span className="text-muted-foreground">⛓ blocks</span>
                <a onClick={() => store.openDetail(d.id)} className="text-status-backlog">
                  #{d.id} {d.title}
                </a>
              </div>
            ))}
            {!deps?.blockers?.length && !deps?.blocked_by?.length && (
              <span className="text-xs text-muted-foreground">No dependencies</span>
            )}
            <AddBlockerInput taskId={task.id} onAdded={refreshDeps} />
          </div>

          <SectionTitle>Attachments</SectionTitle>
          <AttachmentsPanel taskId={task.id} />

          <SectionTitle onClick={() => setShowLogs(!showLogs)}>
            {showLogs ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
            Execution log <span className="font-normal">({logs.length})</span>
          </SectionTitle>
          {showLogs && <LogList logs={logs} />}
        </div>

        <TerminalPane task={task} />
      </div>
    </div>
  );
}
