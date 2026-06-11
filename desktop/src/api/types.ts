export type TaskStatus =
  | "backlog"
  | "queued"
  | "processing"
  | "blocked"
  | "done"
  | "archived";

export interface Task {
  id: number;
  title: string;
  body: string;
  status: TaskStatus;
  type: string;
  project: string;
  executor: string;
  pinned: boolean;
  tags: string;
  permission_mode: string;
  branch_name: string;
  port?: number;
  worktree_path?: string;
  has_executor: boolean;
  effort_level?: string;
  source_branch?: string;
  daemon_session?: string;
  tmux_window_id?: string;
  claude_pane_id?: string;
  shell_pane_id?: string;
  pr_url: string;
  pr_number?: number;
  summary?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface LogLine {
  id: number;
  line_type: string;
  content: string;
  created_at: string;
}

export interface Project {
  id: number;
  name: string;
  path: string;
  aliases: string;
  instructions: string;
  color: string;
  claude_config_dir: string;
  use_worktrees: boolean;
  default_permission_mode: string;
  task_count: number;
}

export interface TaskType {
  id: number;
  name: string;
  label: string;
  instructions: string;
  sort_order: number;
  is_builtin: boolean;
}

export interface ExecutorInfo {
  name: string;
  available: boolean;
  default: boolean;
}

export interface Attachment {
  id: number;
  task_id: number;
  filename: string;
  mime_type: string;
  size: number;
  created_at: string;
}

export interface TerminalInfo {
  daemon_session: string;
  tmux_window_id: string;
  claude_pane_id: string;
  shell_pane_id: string;
  window_target: string;
  window_exists: boolean;
  /** Set when the executor pane is alive but joined into another session
   * (e.g. an open TUI detail view). */
  pane_borrowed_by?: string;
  workdir: string;
}

export interface Dependencies {
  /** Tasks that block this task. */
  blockers: Task[] | null;
  /** Tasks that this task blocks. */
  blocked_by: Task[] | null;
}

export interface TaskDetail {
  task: Task;
  logs: LogLine[];
}

export interface SupervisorStatus {
  port: number;
  ty_path: string | null;
  server_running: boolean;
  daemon_running: boolean;
  server_managed: boolean;
  daemon_managed: boolean;
}

export interface DesktopConfig {
  port: number;
  ty_path: string | null;
}

export interface ToolCheck {
  name: string;
  path: string | null;
}

export interface EnvironmentReport {
  tmux: string | null;
  tmux_version: string | null;
  executors: ToolCheck[];
}

export interface RoutineRun {
  id: number;
  routine: string;
  status: "running" | "ok" | "failed";
  exit_code: number;
  output: string;
  log_path: string;
  started_at: string;
  finished_at?: string;
}

export interface Routine {
  name: string;
  project?: string;
  model: string;
  permission_mode: string;
  timeout: string;
  disabled: boolean;
  schedule?: { backend: string; detail: string };
  last_run?: RoutineRun;
}
