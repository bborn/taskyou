import { useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronRight, GitPullRequest, Pin, Code2 } from "lucide-react";
import { api } from "../api/client";
import { subscribeTaskLogs } from "../api/sse";
import type { ChatMessage, Dependencies, LogLine, Task } from "../api/types";
import { openExternal, openInEditor } from "../tauri";
import { store, useAppState } from "../store";
import { PlacementPanel } from "./PlacementPanel";
import { AttachmentsPanel } from "./AttachmentsPanel";
import { ChatList } from "./ChatList";
import { LogList } from "./LogList";
import { mergeRecentLogs } from "../lib/logs";
import { Markdown } from "./Markdown";
import { TerminalPane } from "./TerminalPane";
import { ReplyComposer } from "./ReplyComposer";
import { useIsMobile } from "../hooks/use-mobile";
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

// Terminal panel sizing: user-draggable (like the TUI's pane divider),
// persisted across sessions, double-click resets.
const TERMINAL_HEIGHT_KEY = "ty-terminal-height";
const DEFAULT_TERMINAL_HEIGHT = 320;
const MIN_TERMINAL_HEIGHT = 140;
const MIN_CONTENT_HEIGHT = 140;

function storedTerminalHeight(): number {
  const raw = Number(localStorage.getItem(TERMINAL_HEIGHT_KEY));
  return Number.isFinite(raw) && raw >= MIN_TERMINAL_HEIGHT ? raw : DEFAULT_TERMINAL_HEIGHT;
}

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
  const isMobile = useIsMobile();
  // The conversation is what you came to read; the execution log is machinery
  // (tool calls and system lines) and stays collapsed until asked for.
  const [showLogs, setShowLogs] = useState(false);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [showChat, setShowChat] = useState(true);
  const [history, setHistory] = useState<LogLine[] | null>(null);
  const [historyBusy, setHistoryBusy] = useState(false);
  const [historyEnd, setHistoryEnd] = useState(false);
  const historyRevision = useRef(0);
  const currentTaskId = useRef(taskId);
  currentTaskId.current = taskId;
  const displayedLogs = history ?? logs;

  // Detail/terminal split: drag the divider to resize, double-click to reset.
  const splitRef = useRef<HTMLDivElement>(null);
  const [terminalHeight, setTerminalHeight] = useState(storedTerminalHeight);
  const [resizing, setResizing] = useState(false);

  const onDividerPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    setResizing(true);
  };
  const onDividerPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!resizing) return;
    const container = splitRef.current;
    if (!container) return;
    const rect = container.getBoundingClientRect();
    const max = Math.max(MIN_TERMINAL_HEIGHT, rect.height - MIN_CONTENT_HEIGHT);
    setTerminalHeight(Math.min(max, Math.max(MIN_TERMINAL_HEIGHT, rect.bottom - e.clientY)));
  };
  const onDividerPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!resizing) return;
    e.currentTarget.releasePointerCapture?.(e.pointerId);
    setResizing(false);
    localStorage.setItem(TERMINAL_HEIGHT_KEY, String(Math.round(terminalHeight)));
  };
  const resetTerminalHeight = () => {
    localStorage.removeItem(TERMINAL_HEIGHT_KEY);
    setTerminalHeight(DEFAULT_TERMINAL_HEIGHT);
  };

  // Keep the local task fresh when the store refreshes (status changes, etc.).
  const storeTask = tasks.find((t) => t.id === taskId);
  useEffect(() => {
    if (storeTask) setTask(storeTask);
  }, [storeTask]);

  useEffect(() => {
    let active = true;
    let unsubscribe: (() => void) | undefined;
    let messageRefresh: ReturnType<typeof setTimeout> | null = null;
    historyRevision.current++;
    setTask((current) => current?.id === taskId ? current : null);
    setLogs([]);
    setHistory(null);
    setHistoryEnd(false);
    setHistoryBusy(false);
    // Establish the initial cursor before subscribing; never replay from zero
    // while the latest-history request is still in flight.
    api.taskDetail(taskId).then((detail) => {
      if (!active) return;
      setTask(detail.task);
      setLogs(mergeRecentLogs([], detail.logs));
      const since = detail.logs[detail.logs.length - 1]?.id ?? 0;
      unsubscribe = subscribeTaskLogs(taskId, since, (batch) => {
        if (!active) return;
        setLogs((prev) => mergeRecentLogs(prev, batch));
        // Log activity means the transcript on disk has almost certainly grown
        // too, so keep the conversation live. Throttled hard: the server
        // re-reads and re-parses the whole session file, which must not happen
        // once per log line.
        if (messageRefresh === null) {
          messageRefresh = setTimeout(() => {
            messageRefresh = null;
            api
              .taskMessages(taskId)
              .then((m) => { if (active) setMessages(m); })
              .catch(() => {});
          }, 4000);
        }
      });
    }).catch((e) => {
      if (active) store.toast({title: `Failed to load #${taskId}`, body: String(e), kind: "error"});
    });
    api.deps(taskId).then((value) => { if (active) setDeps(value); }).catch(() => { if (active) setDeps(null); });
    setMessages([]);
    api.taskMessages(taskId)
      .then((m) => { if (active) setMessages(m); })
      .catch(() => { if (active) setMessages([]); });
    return () => {
      active = false;
      unsubscribe?.();
      if (messageRefresh !== null) clearTimeout(messageRefresh);
    };
  }, [taskId]);

  async function loadOlderLogs() {
    if (!displayedLogs.length || historyBusy) return;
    const id = taskId;
    const revision = ++historyRevision.current;
    setHistoryBusy(true);
    try {
      const page = await api.taskLogsBefore(id, displayedLogs[0].id);
      if (currentTaskId.current !== id || historyRevision.current !== revision) return;
      setHistoryEnd(page.length < 200);
      if (page.length) setHistory(page);
    } catch (e) {
      if (currentTaskId.current === id) store.toast({title: "Could not load older logs", body: String(e), kind: "error"});
    } finally {
      if (currentTaskId.current === id && historyRevision.current === revision) setHistoryBusy(false);
    }
  }

  if (!task) {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        Loading task #{taskId}…
      </div>
    );
  }

  const blocked = task.status === "blocked";
  const refreshDeps = () => api.deps(task.id).then(setDeps).catch(() => {});

  // The executor's actual conversation, read from the Claude session
  // transcript. It was never in task_logs — that table only ever held tool
  // calls and system lines, so the prose had nowhere to go.
  //
  // `follow` on a phone: 135 turns deep, opening at the oldest message means
  // scrolling the whole history to find what the agent is waiting on.
  const conversationSection = (
    <>
      <SectionTitle onClick={() => setShowChat(!showChat)}>
        {showChat ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        Conversation <span className="font-normal">({messages.length})</span>
      </SectionTitle>
      {showChat && <ChatList messages={messages} follow={isMobile} />}
    </>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-surface-1 px-3 py-2.5 md:px-4">
        <Badge variant="outline" className={STATUS_BADGE[task.status] ?? ""}>
          {task.status}
        </Badge>
        <span className="font-mono text-[11px] text-muted-foreground">#{task.id}</span>
        <span
          className={
            isMobile
              ? // Title first on a phone: the badges and buttons used to push it
                // onto a third row, so you scrolled before reading what this is.
                "order-first line-clamp-3 w-full text-[15px] leading-snug font-semibold"
              : "max-w-[44ch] truncate text-sm font-semibold"
          }
          title={task.title}
        >
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

        {/* The executor decides what actually runs when you tap Execute in the
            phone composer, so it belongs on the phone too. */}
        <Select
          value={task.executor || "claude"}
          onValueChange={async (v) => {
            await api.updateTask(task.id, { executor: v }).catch(() => {});
            void store.refreshTasks();
          }}
        >
          <SelectTrigger size="sm" className={isMobile ? "h-9 w-28" : "w-32"} title="Executor">
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

        {!isMobile &&
          (blocked ? (
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
          ))}
        <Button variant="outline" size="sm" onClick={() => store.setForm({ kind: "edit", taskId: task.id })}>
          Edit
        </Button>
        {task.worktree_path && !isMobile && (
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
      {task.stand && (
        <div
          className={`shrink-0 truncate border-b bg-surface-1 px-4 py-1.5 text-[12.5px] ${
            blocked ? "text-status-blocked" : "text-muted-foreground"
          }`}
          title={task.stand}
        >
          {task.stand}
        </div>
      )}

      <div ref={splitRef} className="flex min-h-0 flex-1 flex-col">
        <div className="min-h-[140px] min-w-0 flex-1 overflow-x-hidden overflow-y-auto px-4 py-3.5 break-words select-text md:px-5">
          {/* Phone: the conversation is why you opened this, so it comes before
              the ticket body, placement, dependencies and attachments. */}
          {isMobile && conversationSection}

          {task.body ? (
            <Markdown source={task.body} />
          ) : (
            <span className="text-xs text-muted-foreground">No description</span>
          )}

          {task.summary && !task.stand && (
            <>
              <SectionTitle>Summary</SectionTitle>
              <Markdown source={task.summary} />
            </>
          )}

          <PlacementPanel key={task.id} taskId={task.id} />
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

          {/* Desktop keeps it in place; the phone hoists it to the top. */}
          {!isMobile && conversationSection}

          <SectionTitle onClick={() => setShowLogs(!showLogs)}>
            {showLogs ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
            Execution log <span className="font-normal">({displayedLogs.length})</span>
          </SectionTitle>
          {showLogs && <>
            <div className="mb-2 flex gap-2">
              <Button size="sm" variant="outline" disabled={historyBusy || historyEnd || displayedLogs.length === 0} onClick={() => void loadOlderLogs()}>
                {historyBusy ? "Loading…" : "Older logs"}
              </Button>
              {history && <Button size="sm" variant="outline" onClick={() => { historyRevision.current++; setHistoryBusy(false); setHistory(null); setHistoryEnd(false); }}>Back to live</Button>}
            </div>
            <LogList logs={displayedLogs} follow={history === null} />
          </>}
        </div>

        {/* A phone gets a reply box where the desktop gets the live terminal:
            xterm needs a keyboard and ~80 columns. */}
        {isMobile ? (
          <ReplyComposer task={task} />
        ) : (
          <>
            <div
              role="separator"
              aria-orientation="horizontal"
              title="Drag to resize · double-click to reset"
              className="group relative z-10 -my-1 h-2.5 shrink-0 cursor-row-resize touch-none"
              onPointerDown={onDividerPointerDown}
              onPointerMove={onDividerPointerMove}
              onPointerUp={onDividerPointerUp}
              onPointerCancel={onDividerPointerUp}
              onDoubleClick={resetTerminalHeight}
            >
              <div
                className={`pointer-events-none absolute inset-x-0 top-1/2 -translate-y-1/2 transition-[background-color,height] ${
                  resizing
                    ? "h-[3px] bg-status-backlog"
                    : "h-px bg-border group-hover:h-[3px] group-hover:bg-status-backlog/60"
                }`}
              />
            </div>

            <div
              className="flex min-h-[140px] flex-col"
              style={{ height: terminalHeight, maxHeight: "calc(100% - 140px)" }}
            >
              <TerminalPane task={task} />
            </div>
          </>
        )}
      </div>
    </div>
  );
}
