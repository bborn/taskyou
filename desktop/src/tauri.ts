// Bridge to the Rust core. Falls back gracefully when running in a plain
// browser (vite dev without Tauri) so the data UI remains testable.
import { invoke, Channel } from "@tauri-apps/api/core";
import type { DesktopConfig, EnvironmentReport, SupervisorStatus } from "./api/types";

export type PtyEvent = { type: "data"; data: string } | { type: "exit" };

export function inTauri(): boolean {
  return "__TAURI_INTERNALS__" in window;
}

export interface SpawnOptions {
  command: string[];
  cwd?: string;
  env?: Record<string, string>;
  cols: number;
  rows: number;
  kill_tmux_session?: string;
}

export async function ptySpawn(
  opts: SpawnOptions,
  onEvent: (event: PtyEvent) => void,
): Promise<number> {
  const channel = new Channel<PtyEvent>();
  channel.onmessage = onEvent;
  return invoke<number>("pty_spawn", { opts, onEvent: channel });
}

export async function ptyWrite(id: number, data: string): Promise<void> {
  return invoke("pty_write", { id, data });
}

export async function ptyResize(id: number, cols: number, rows: number): Promise<void> {
  return invoke("pty_resize", { id, cols, rows });
}

export async function ptyKill(id: number): Promise<void> {
  return invoke("pty_kill", { id });
}

export interface AttachResult {
  pty_id: number;
  view_session: string;
}

export async function attachTaskTerminal(
  taskId: number,
  daemonSession: string,
  window: string,
  pane: string | null,
  cols: number,
  rows: number,
  onEvent: (event: PtyEvent) => void,
): Promise<AttachResult> {
  const channel = new Channel<PtyEvent>();
  channel.onmessage = onEvent;
  return invoke<AttachResult>("attach_task_terminal", {
    taskId,
    daemonSession,
    window,
    pane,
    cols,
    rows,
    onEvent: channel,
  });
}

export async function supervisorStatus(): Promise<SupervisorStatus> {
  return invoke("supervisor_status");
}

export async function supervisorEnsure(): Promise<SupervisorStatus> {
  return invoke("supervisor_ensure");
}

export async function supervisorGetConfig(): Promise<DesktopConfig> {
  return invoke("supervisor_get_config");
}

export async function supervisorSetConfig(config: DesktopConfig): Promise<void> {
  return invoke("supervisor_set_config", { config });
}

export async function openExternal(target: string): Promise<void> {
  if (!inTauri()) {
    window.open(target, "_blank");
    return;
  }
  return invoke("open_external", { target });
}

export async function openInEditor(path: string): Promise<void> {
  return invoke("open_in_editor", { path });
}

export async function notify(title: string, body: string): Promise<void> {
  if (!inTauri()) return;
  // Native-app etiquette: when the window is focused the in-app toast is
  // enough; only notify the system when the user is elsewhere.
  if (document.hasFocus()) return;
  try {
    const { isPermissionGranted, requestPermission, sendNotification } = await import(
      "@tauri-apps/plugin-notification"
    );
    let granted = await isPermissionGranted();
    if (!granted) {
      granted = (await requestPermission()) === "granted";
    }
    if (granted) sendNotification({ title, body });
  } catch {
    // notifications are best-effort
  }
}

export async function checkEnvironment(): Promise<EnvironmentReport> {
  return invoke("check_environment");
}
