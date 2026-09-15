import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { api, apiBase } from "../api/client";
import type { Task } from "../api/types";
import { Button } from "./ui/button";

/** Independent pane view. It never zooms, selects, resizes, or kills tmux panes.
 * The task owns terminal dimensions; overflow is scrollable in smaller views. */
export function PaneMirror({ task, pane }: { task: Task; pane: "agent" | "shell" }) {
  const host = useRef<HTMLDivElement>(null);
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState("Connecting…");
  useEffect(() => {
    let stopped = false;
    let socket: WebSocket | undefined;
    let term: Terminal | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function connect() {
      try {
        const info = await api.terminalInfo(task.id);
        if (stopped) return;
        if (info.error) throw new Error(info.error);
        if (!info.window_exists) {
          setStatus("No session running. Start the task to connect.");
          timer = setTimeout(() => void connect(), 3000);
          return;
        }
        if (pane === "shell") await api.ensureShellPane(task.id);
        if (stopped || !host.current) return;
        term = new Terminal({
          allowProposedApi: true, fontSize: 12, cursorBlink: true,
          fontFamily: '"SF Mono", Menlo, monospace', scrollback: 1000,
          theme: { background: "#151820", foreground: "#c5cbe3", cursor: "#c5cbe3" },
        });
        term.loadAddon(new Unicode11Addon());
        term.unicode.activeVersion = "11";
        term.open(host.current);
        socket = new WebSocket(`${apiBase().replace(/^http/, "ws")}/api/tasks/${task.id}/terminal?pane=${pane}&resize=0`);
        const ws = socket;
        socket.onmessage = (event) => {
          if (stopped || !term) return;
          const data = String(event.data);
          if (data.startsWith("{")) {
            try {
              const size = JSON.parse(data);
              if (size.type === "size") { term.resize(size.cols, size.rows); return; }
            } catch { /* ordinary terminal output */ }
          }
          term.write(data);
        };
        socket.onopen = () => { if (!stopped) setStatus(""); };
        socket.onerror = () => { if (!stopped) setStatus("Connection failed. Retry to reconnect."); };
        socket.onclose = () => { if (!stopped) setStatus("Disconnected. The session continues running."); };
        term.onData((data) => { if (ws.readyState === WebSocket.OPEN) ws.send(data); });
        // Deliberately do not focus: opening a tab must not steal chat input.
      } catch (error) {
        if (!stopped) setStatus(error instanceof Error ? error.message : String(error));
      }
    }
    void connect();
    return () => { stopped = true; clearTimeout(timer); socket?.close(); term?.dispose(); };
  }, [task.id, pane, attempt]);
  return <div className="relative flex min-h-0 flex-1 flex-col bg-[#151820] text-[#c5cbe3]">
    {status && <div role="status" className="flex items-center justify-between gap-3 border-b border-white/10 px-3 py-2 text-xs">
      {status}<Button variant="ghost" size="sm" onClick={() => setAttempt((n) => n + 1)}>Retry</Button>
    </div>}
    <div ref={host} className="min-h-0 flex-1 overflow-auto p-2" aria-label={`${pane === "agent" ? "Agent" : "Shell"} terminal`} />
  </div>;
}
