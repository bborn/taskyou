import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { api } from "../api/client";
import type { Task } from "../api/types";
import { attachTaskTerminal, inTauri, ptyKill, ptyResize, ptyWrite } from "../tauri";
import { store } from "../store";

type TermState =
  | { kind: "loading" }
  | { kind: "attached" }
  | { kind: "no-window" }
  | { kind: "exited" }
  | { kind: "unsupported" }
  | { kind: "error"; message: string };

function base64ToBytes(data: string): Uint8Array {
  const bin = atob(data);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

const XTERM_THEME = {
  background: "#131318",
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

/**
 * The executor terminal: a real xterm.js terminal backed by a Rust PTY running
 * a tmux client attached (via a grouped view session) to the task's daemon
 * window. Fully interactive — keystrokes, mouse, resize all flow through.
 */
export function TerminalPane({ task }: { task: Task }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const ptyIdRef = useRef<number | null>(null);
  const [state, setState] = useState<TermState>({ kind: "loading" });
  const [starting, setStarting] = useState(false);

  const detach = useCallback((showExited = false) => {
    if (ptyIdRef.current !== null) {
      void ptyKill(ptyIdRef.current);
      ptyIdRef.current = null;
      if (showExited) setState({ kind: "exited" });
    }
  }, []);

  const attach = useCallback(async () => {
    if (!inTauri()) {
      setState({ kind: "unsupported" });
      return;
    }
    setState({ kind: "loading" });
    try {
      const info = await api.terminalInfo(task.id);
      if (!info.window_exists) {
        setState({ kind: "no-window" });
        return;
      }

      // window_target is "session:index"; attach the grouped view to that
      // session and select the same window.
      const sep = info.window_target.indexOf(":");
      const daemonSession = info.window_target.slice(0, sep);
      const windowSpec = info.window_target.slice(sep + 1);

      const host = hostRef.current;
      if (!host) return;

      // Fresh terminal per attach.
      termRef.current?.dispose();
      const term = new Terminal({
        theme: XTERM_THEME,
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
  }, [task.id]);

  // Initial attach + cleanup on unmount/task change.
  useEffect(() => {
    void attach();
    return () => {
      detach();
      termRef.current?.dispose();
      termRef.current = null;
    };
  }, [attach, detach]);

  // Refit on container resize.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const observer = new ResizeObserver(() => fitRef.current?.fit());
    observer.observe(host);
    return () => observer.disconnect();
  }, []);

  // While the daemon hasn't created the window yet (queued/just-executed
  // tasks), poll until it appears, then attach automatically.
  useEffect(() => {
    if (state.kind !== "no-window") return;
    if (!(task.status === "queued" || task.status === "processing")) return;
    const id = setInterval(async () => {
      try {
        const info = await api.terminalInfo(task.id);
        if (info.window_exists) {
          clearInterval(id);
          void attach();
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

  return (
    <div className="terminal-area">
      <div className="terminal-toolbar">
        <span>Executor terminal</span>
        {state.kind === "attached" && <span style={{ color: "var(--green)" }}>● live</span>}
        <div className="spacer" style={{ flex: 1 }} />
        {state.kind === "attached" && (
          <button className="icon-btn" title="Detach" onClick={() => detach(true)}>
            ⏏
          </button>
        )}
      </div>

      <div
        className="terminal-host"
        ref={hostRef}
        style={{ display: state.kind === "attached" ? "block" : "none" }}
      />

      {state.kind !== "attached" && (
        <div className="terminal-placeholder">
          {state.kind === "loading" && <span>Connecting…</span>}
          {state.kind === "unsupported" && (
            <span>Terminal requires the desktop app (running in browser)</span>
          )}
          {state.kind === "no-window" && (
            <>
              <span>
                {task.status === "queued" || task.status === "processing"
                  ? "Waiting for the executor to start…"
                  : "No executor session running for this task"}
              </span>
              {task.status !== "queued" && task.status !== "processing" && (
                <button className="btn primary" disabled={starting} onClick={() => void startSession()}>
                  {starting ? "Starting…" : `Start ${task.executor || "claude"} session`}
                </button>
              )}
            </>
          )}
          {state.kind === "exited" && (
            <>
              <span>Terminal detached</span>
              <button className="btn primary" onClick={() => void attach()}>
                Reattach
              </button>
            </>
          )}
          {state.kind === "error" && (
            <>
              <span style={{ color: "var(--red)" }}>{state.message}</span>
              <button className="btn" onClick={() => void attach()}>
                Retry
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
