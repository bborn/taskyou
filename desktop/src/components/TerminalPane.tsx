import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SendHorizontal } from "lucide-react";
import { api, apiBase } from "../api/client";
import type { Task } from "../api/types";
import { attachTaskTerminal, inTauri, ptyKill, ptyResize, ptyWrite } from "../tauri";
import { store, useAppState } from "../store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

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

const XTERM_DARK = {
  background: "#16161e",
  foreground: "#c0caf5",
  cursor: "#c0caf5",
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
  background: "#fafafa",
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
 * The executor terminal: a real xterm.js terminal backed by a Rust PTY running
 * a tmux client attached (via a grouped view session) to the task's daemon
 * window. Fully interactive — keystrokes, mouse, resize all flow through.
 */
export function TerminalPane({ task }: { task: Task }) {
  const { theme } = useAppState();
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const ptyIdRef = useRef<number | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [state, setState] = useState<TermState>({ kind: "loading" });
  const [starting, setStarting] = useState(false);
  const [composer, setComposer] = useState("");
  const [sending, setSending] = useState(false);
  const composerRef = useRef<HTMLInputElement>(null);

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
      fontFamily: '"SF Mono", ui-monospace, Menlo, monospace',
      fontSize: 12.5,
      cursorBlink: true,
      macOptionIsMeta: true,
      scrollback: 1000,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;
    return term;
  }, []);

  /** Browser fallback: no PTY available, so mirror the executor pane over the
   * server's capture-pane WebSocket. Input flows via tmux send-keys; works by
   * pane ID, so it keeps working even while the TUI borrows the pane. */
  const attachBrowser = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const info = await api.terminalInfo(task.id);
      if (!info.claude_pane_id) {
        setState({ kind: "no-window" });
        return;
      }
      const term = buildTerm();
      if (!term) return;

      const ws = new WebSocket(`${apiBase().replace(/^http/, "ws")}/api/tasks/${task.id}/terminal`);
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
  }, [task.id, buildTerm]);

  const attach = useCallback(async () => {
    if (!inTauri()) {
      return attachBrowser();
    }
    setState({ kind: "loading" });
    try {
      const info = await api.terminalInfo(task.id);
      if (!info.window_exists) {
        setState(
          info.pane_borrowed_by
            ? { kind: "borrowed", by: info.pane_borrowed_by }
            : { kind: "no-window" },
        );
        return;
      }

      // window_target is "session:index"; attach the grouped view to that
      // session and select the same window.
      const sep = info.window_target.indexOf(":");
      const daemonSession = info.window_target.slice(0, sep);
      const windowSpec = info.window_target.slice(sep + 1);

      const term = buildTerm();
      if (!term) return;

      const result = await attachTaskTerminal(
        task.id,
        daemonSession,
        windowSpec,
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
  }, [task.id, buildTerm, attachBrowser]);

  // Initial attach + cleanup on unmount/task change.
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
        const info = await api.terminalInfo(task.id);
        if (info.window_exists) {
          clearInterval(id);
          void attach();
        } else if (state.kind === "borrowed" && !info.pane_borrowed_by) {
          setState({ kind: "no-window" });
        }
      } catch {
        // keep polling
      }
    }, 2000);
    return () => clearInterval(id);
  }, [state.kind, task.id, task.status, attach]);

  /** Composer: send a line to the agent via the server's input endpoint
   * (tmux send-keys + Enter against the executor pane) — the same transport
   * the WebSocket mirror uses for keystrokes, minus the fiddly xterm typing. */
  async function sendComposer() {
    const message = composer.trim();
    if (!message || sending) return;
    setSending(true);
    try {
      await api.sendInput(task.id, message);
      setComposer("");
    } catch (e) {
      store.toast({
        title: "Failed to send input",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    } finally {
      setSending(false);
      composerRef.current?.focus();
    }
  }

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

  return (
    <div className="flex min-h-[160px] flex-1 flex-col bg-surface-1">
      <div className="flex items-center gap-2 border-b px-3 py-1 text-[11px] text-muted-foreground">
        <span>Executor terminal</span>
        {state.kind === "attached" && (
          <span className="text-status-processing">{inTauri() ? "● live" : "● live (mirror)"}</span>
        )}
        <div className="flex-1" />
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

      {state.kind === "attached" && (
        <form
          className="flex shrink-0 items-center gap-1.5 border-t px-2 py-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            void sendComposer();
          }}
        >
          <Input
            ref={composerRef}
            value={composer}
            onChange={(e) => setComposer(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                // Hand focus back to the terminal instead of letting the
                // app-level Escape handler navigate away from the detail view.
                e.preventDefault();
                e.stopPropagation();
                termRef.current?.focus();
              }
            }}
            placeholder="Send input to terminal…"
            spellCheck={false}
            autoComplete="off"
            className="h-7 flex-1 text-xs shadow-none md:text-xs"
          />
          <Button
            type="submit"
            variant="ghost"
            size="icon"
            className="size-7 shrink-0"
            title="Send to terminal (Enter)"
            disabled={sending || composer.trim() === ""}
          >
            <SendHorizontal className="size-3.5" />
          </Button>
        </form>
      )}

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
