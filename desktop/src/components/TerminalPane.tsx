import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { api, apiBase } from "../api/client";
import type { Task, TerminalInfo } from "../api/types";
import { attachTaskTerminal, inTauri, ptyKill, ptyResize, ptyWrite } from "../tauri";
import { store, useAppState } from "../store";
import { Button } from "@/components/ui/button";

type TerminalTab = "agent" | "shell";

type TermState =
  | { kind: "loading" }
  | { kind: "attached" }
  | { kind: "no-window" }
  | { kind: "borrowed"; by: string }
  | { kind: "exited" }
  | { kind: "error"; message: string };

function base64ToBytes(data: string): Uint8Array {
  const bin = atob(data);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

// ANSI palettes (Tokyo Night-ish). The background stays transparent so the
// app's CSS theme variables (light/dark, vibrancy material) show through and
// the terminal blends with the rest of the GUI instead of being a hard-edged
// foreign rectangle.
const TRANSPARENT = "#00000000";

const XTERM_DARK = {
  background: TRANSPARENT,
  foreground: "#c5cbe3",
  cursor: "#c5cbe3",
  selectionBackground: "#33467c",
  black: "#15161e",
  red: "#f7768e",
  green: "#9ece6a",
  yellow: "#e0af68",
  blue: "#7aa2f7",
  magenta: "#bb9af7",
  cyan: "#7dcfff",
  white: "#a9b1d6",
  brightBlack: "#414868",
  brightRed: "#f7768e",
  brightGreen: "#9ece6a",
  brightYellow: "#e0af68",
  brightBlue: "#7aa2f7",
  brightMagenta: "#bb9af7",
  brightCyan: "#7dcfff",
  brightWhite: "#c0caf5",
};

const XTERM_LIGHT = {
  background: TRANSPARENT,
  foreground: "#343b58",
  cursor: "#343b58",
  selectionBackground: "#b6bdd9",
  black: "#0f0f14",
  red: "#8c4351",
  green: "#485e30",
  yellow: "#8f5e15",
  blue: "#34548a",
  magenta: "#5a4a78",
  cyan: "#0f4b6e",
  white: "#828594",
  brightBlack: "#5e626e",
  brightRed: "#8c4351",
  brightGreen: "#485e30",
  brightYellow: "#8f5e15",
  brightBlue: "#34548a",
  brightMagenta: "#5a4a78",
  brightCyan: "#0f4b6e",
  brightWhite: "#343b58",
};

function currentXtermTheme() {
  return document.documentElement.classList.contains("dark") ? XTERM_DARK : XTERM_LIGHT;
}

/**
 * The task terminal panel: "Agent" and "Shell" tabs above a real xterm.js
 * terminal. Each tab is its own xterm instance attached to its own tmux pane
 * — the executor pane or the task's workdir shell pane — instead of mirroring
 * the raw side-by-side tmux window layout.
 */
export function TerminalPane({ task }: { task: Task }) {
  const [tab, setTab] = useState<TerminalTab>("agent");
  // Remount per task and per tab: only the active tab holds a live attach
  // (single-pane tmux views can't be attached concurrently — zoom is a
  // window-level flag shared across the session group).
  return <TerminalSurface key={`${task.id}:${tab}`} task={task} tab={tab} onTabChange={setTab} />;
}

function TabButton({
  active,
  live,
  onClick,
  children,
}: {
  active: boolean;
  live: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`relative -mb-px flex items-center gap-1.5 border-b-2 px-2.5 py-1.5 text-[11px] font-medium transition-colors ${
        active
          ? "border-foreground/70 text-foreground"
          : "border-transparent text-muted-foreground hover:text-foreground"
      }`}
    >
      <span
        className={`size-1.5 rounded-full ${live ? "bg-status-processing" : "bg-muted-foreground/30"}`}
      />
      {children}
    </button>
  );
}

function TerminalSurface({
  task,
  tab,
  onTabChange,
}: {
  task: Task;
  tab: TerminalTab;
  onTabChange: (tab: TerminalTab) => void;
}) {
  const { theme } = useAppState();
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const ptyIdRef = useRef<number | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [state, setState] = useState<TermState>({ kind: "loading" });
  const [info, setInfo] = useState<TerminalInfo | null>(null);
  const [starting, setStarting] = useState(false);

  const detach = useCallback((showExited = false) => {
    if (ptyIdRef.current !== null) {
      void ptyKill(ptyIdRef.current);
      ptyIdRef.current = null;
      if (showExited) setState({ kind: "exited" });
    }
    if (wsRef.current) {
      wsRef.current.onclose = null;
      wsRef.current.close();
      wsRef.current = null;
      if (showExited) setState({ kind: "exited" });
    }
  }, []);

  const buildTerm = useCallback(() => {
    const host = hostRef.current;
    if (!host) return null;
    termRef.current?.dispose();
    const term = new Terminal({
      theme: currentXtermTheme(),
      allowTransparency: true,
      fontFamily:
        '"SF Mono", SFMono-Regular, ui-monospace, "Cascadia Code", "JetBrains Mono", Menlo, Monaco, "DejaVu Sans Mono", monospace',
      fontSize: 12.5,
      cursorBlink: true,
      macOptionIsMeta: true,
      scrollback: 1000,
    });
    // Unicode 11 width tables: agent CLIs lean on symbols (⏺, ✶, spinners)
    // that render as gaps/overlaps under xterm's legacy width handling.
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = "11";
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;
    return term;
  }, []);

  /** Resolve terminal info, ensuring the shell pane exists for the Shell tab.
   * Returns null (after setting a waiting state) when not attachable. */
  const resolveInfo = useCallback(async (): Promise<TerminalInfo | null> => {
    const current = await api.terminalInfo(task.id);
    if (!current.window_exists) {
      setInfo(current);
      setState(
        current.pane_borrowed_by
          ? { kind: "borrowed", by: current.pane_borrowed_by }
          : { kind: "no-window" },
      );
      return null;
    }
    const resolved = tab === "shell" ? await api.ensureShellPane(task.id) : current;
    setInfo(resolved);
    return resolved;
  }, [task.id, tab]);

  /** Browser fallback: no PTY available, so mirror the tmux pane over the
   * server's capture-pane WebSocket. Input flows via tmux send-keys; works by
   * pane ID, so it keeps working even while the TUI borrows the pane. */
  const attachBrowser = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const resolved = await resolveInfo();
      if (!resolved) return;
      const paneId = tab === "shell" ? resolved.shell_pane_id : resolved.claude_pane_id;
      if (!paneId) {
        setState({ kind: "no-window" });
        return;
      }
      const term = buildTerm();
      if (!term) return;

      const paneQuery = tab === "shell" ? "?pane=shell" : "";
      const ws = new WebSocket(
        `${apiBase().replace(/^http/, "ws")}/api/tasks/${task.id}/terminal${paneQuery}`,
      );
      wsRef.current = ws;
      ws.onmessage = (event) => {
        const data = String(event.data);
        if (data.startsWith("{")) {
          try {
            const msg = JSON.parse(data);
            if (msg.type === "size") return; // we drive sizing via resize messages
          } catch {
            // fall through: screen content that happens to start with "{"
          }
        }
        term.write(data);
      };
      ws.onclose = () => {
        if (wsRef.current === ws) {
          wsRef.current = null;
          setState({ kind: "exited" });
        }
      };
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      };
      term.onData((data) => {
        if (ws.readyState === WebSocket.OPEN) ws.send(data);
      });
      term.onResize(({ cols, rows }) => {
        if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
      });
      term.focus();
      setState({ kind: "attached" });
    } catch (e) {
      setState({ kind: "error", message: e instanceof Error ? e.message : String(e) });
    }
  }, [task.id, tab, buildTerm, resolveInfo]);

  const attach = useCallback(async () => {
    if (!inTauri()) {
      return attachBrowser();
    }
    setState({ kind: "loading" });
    try {
      const resolved = await resolveInfo();
      if (!resolved) return;

      // window_target is "session:index"; attach the grouped view to that
      // session, select the same window, and zoom this tab's pane.
      const sep = resolved.window_target.indexOf(":");
      const daemonSession = resolved.window_target.slice(0, sep);
      const windowSpec = resolved.window_target.slice(sep + 1);
      const paneId = tab === "shell" ? resolved.shell_pane_id : resolved.claude_pane_id;

      const term = buildTerm();
      if (!term) return;

      const result = await attachTaskTerminal(
        task.id,
        daemonSession,
        windowSpec,
        paneId || null,
        term.cols,
        term.rows,
        (event) => {
          if (event.type === "data") {
            term.write(base64ToBytes(event.data));
          } else if (event.type === "exit") {
            if (ptyIdRef.current !== null) {
              ptyIdRef.current = null;
              setState({ kind: "exited" });
            }
          }
        },
      );
      ptyIdRef.current = result.pty_id;

      term.onData((data) => {
        if (ptyIdRef.current !== null) void ptyWrite(ptyIdRef.current, data);
      });
      term.onResize(({ cols, rows }) => {
        if (ptyIdRef.current !== null) void ptyResize(ptyIdRef.current, cols, rows);
      });
      term.focus();
      setState({ kind: "attached" });
    } catch (e) {
      setState({ kind: "error", message: e instanceof Error ? e.message : String(e) });
    }
  }, [task.id, tab, buildTerm, attachBrowser, resolveInfo]);

  // Initial attach + cleanup on unmount/task/tab change.
  useEffect(() => {
    void attach();
    return () => {
      detach();
      termRef.current?.dispose();
      termRef.current = null;
    };
  }, [attach, detach]);

  // Re-theme the live terminal when the app theme changes.
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = currentXtermTheme();
  }, [theme]);

  // Refit on container resize.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const observer = new ResizeObserver(() => fitRef.current?.fit());
    observer.observe(host);
    return () => observer.disconnect();
  }, []);

  // Poll until the daemon window is attachable: covers tasks whose executor
  // hasn't started yet AND panes temporarily borrowed by an open TUI detail
  // view (the daemon window is rebuilt when the TUI releases them).
  useEffect(() => {
    const waiting =
      state.kind === "borrowed" ||
      (state.kind === "no-window" && (task.status === "queued" || task.status === "processing"));
    if (!waiting) return;
    const id = setInterval(async () => {
      try {
        const current = await api.terminalInfo(task.id);
        setInfo(current);
        if (current.window_exists) {
          clearInterval(id);
          void attach();
        } else if (state.kind === "borrowed" && !current.pane_borrowed_by) {
          setState({ kind: "no-window" });
        }
      } catch {
        // keep polling
      }
    }, 2000);
    return () => clearInterval(id);
  }, [state.kind, task.id, task.status, attach]);

  async function startSession() {
    setStarting(true);
    try {
      await api.ensureSession(task.id);
      await attach();
    } catch (e) {
      store.toast({
        title: "Failed to start session",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
      setState({ kind: "no-window" });
    } finally {
      setStarting(false);
    }
  }

  const windowLive = info?.window_exists ?? false;
  const agentLive =
    (tab === "agent" && state.kind === "attached") || (windowLive && !!info?.claude_pane_id);
  const shellLive =
    (tab === "shell" && state.kind === "attached") || (windowLive && !!info?.shell_pane_id);

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface-1">
      <div className="flex shrink-0 items-center gap-1 border-b px-2 text-[11px] text-muted-foreground">
        <TabButton active={tab === "agent"} live={agentLive} onClick={() => onTabChange("agent")}>
          Agent
        </TabButton>
        <TabButton active={tab === "shell"} live={shellLive} onClick={() => onTabChange("shell")}>
          Shell
        </TabButton>
        <div className="flex-1" />
        {state.kind === "attached" && (
          <span className="text-status-processing">{inTauri() ? "● live" : "● live (mirror)"}</span>
        )}
        {state.kind === "attached" && (
          <Button variant="ghost" size="icon" className="size-5" title="Detach" onClick={() => detach(true)}>
            ⏏
          </Button>
        )}
      </div>

      <div
        className="terminal-host"
        ref={hostRef}
        style={{ display: state.kind === "attached" ? "block" : "none" }}
      />

      {state.kind !== "attached" && (
        <div className="flex flex-1 flex-col items-center justify-center gap-2.5 text-muted-foreground">
          {state.kind === "loading" && <span>Connecting…</span>}
          {state.kind === "borrowed" && (
            <>
              <span className="max-w-md text-center">
                The executor is currently attached to the TUI ({state.by}). It will appear here
                automatically when the TUI releases it — close the task's detail view there.
              </span>
              <span className="text-status-processing">● running elsewhere</span>
            </>
          )}
          {state.kind === "no-window" && (
            <>
              <span>
                {task.status === "queued" || task.status === "processing"
                  ? "Waiting for the executor to start…"
                  : tab === "shell"
                    ? "No session running for this task — the shell opens alongside the executor"
                    : "No executor session running for this task"}
              </span>
              {task.status !== "queued" && task.status !== "processing" && (
                <Button disabled={starting} onClick={() => void startSession()}>
                  {starting ? "Starting…" : `Start ${task.executor || "claude"} session`}
                </Button>
              )}
            </>
          )}
          {state.kind === "exited" && (
            <>
              <span>Terminal detached</span>
              <Button onClick={() => void attach()}>Reattach</Button>
            </>
          )}
          {state.kind === "error" && (
            <>
              <span className="text-destructive">{state.message}</span>
              <Button variant="outline" onClick={() => void attach()}>
                Retry
              </Button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
