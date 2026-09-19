export type TaskStatus =
  | "backlog"
  | "queued"
  | "processing"
  | "blocked"
  | "done"
  | "archived";

export interface Task {
 placement_target?: string;
 placement_reason?: string;
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
  pr?: PRStatus;
  summary?: string;
  stand?: string;
  /** What the agent is blocked on asking (taskyou_needs_input). Only on a
   * blocked task. */
  question?: PendingQuestion;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
}

export type QuestionKind = "text" | "choice" | "multi_choice" | "confirm";

export interface QuestionOption {
  label: string;
  description?: string;
}

/** A blocked task's pending question. `options` are the answers to pick,
 * numbered from 1 in this order — a confirm's are Yes and No; a text question
 * has none. Mirrors questionJSON in internal/web/questions.go. */
export interface PendingQuestion {
  id: number;
  question: string;
  kind: QuestionKind;
  options: QuestionOption[];
  allow_other: boolean;
  created_at: string;
}

/** POST /api/tasks/{id}/answer. `choices` are 1-based option numbers. */
export interface QuestionAnswer {
  question_id: number;
  choices?: number[];
  other?: string;
}

export type PRState = "open" | "draft" | "merged" | "closed";
export type PRCheckState = "passing" | "failing" | "pending" | "";

// Live PR badge payload mirrored from the cached github.PRInfo on the server.
export interface PRStatus {
  number: number;
  url: string;
  state: PRState;
  check_state: PRCheckState;
  mergeable: string;
  additions: number;
  deletions: number;
}

export interface LogLine {
  id: number;
  line_type: string;
  content: string;
  created_at: string;
}

/** One turn of the executor's actual conversation, read from the Claude session
 * transcript. `task_logs` only ever held the machinery (tool calls, system
 * lines) — the prose never went to the database. */
export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  /** Prose only. Reasoning is summarised by `thinking` rather than inlined. */
  text: string;
  /** Names of tools used in this turn, in order, e.g. ["Read", "Bash"]. */
  tools: string[];
  /** Number of reasoning blocks in this turn; the text itself is not sent. */
  thinking: number;
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
  remote_host?: string;
  error?: string;
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

// PlacementHost is one machine the placement plugin offers for a new task. An
// empty list — no plugin, or no host serving the project — is why the new-task
// form shows no host picker at all.
export interface PlacementHost {
  name: string;
  target: string;
  workdir: string;
  detail?: string;
}

export interface Placement {
 target: string;
 workdir: string;
 remote_worktree: string;
 reason: string;
 decided: boolean;
 health: { state: string; last_seen?: string; problem?: string };
}

// CatalogPlugin is one row of the plugin browser: a catalog entry, a plugin
// installed from outside the catalog (in_catalog false), or both.
export interface CatalogPlugin {
  id: string;
  name: string;
  description: string;
  author?: string;
  category?: string;
  tags?: string[];
  provides?: string[];
  requires?: string[];
  source?: string;
  subdir?: string;
  homepage?: string;
  installed: boolean;
  in_catalog: boolean;
}

export interface PluginCatalog {
  plugins: CatalogPlugin[];
  // stale: the catalog was served from cache or the copy shipped with ty, so a
  // refresh is worth offering.
  stale: boolean;
}

export interface InstalledPlugin {
  name: string;
  version?: string;
  description?: string;
  dir: string;
  hooks?: string[];
  actions?: string[];
  workflows?: string[];
  routines?: string[];
  services?: string[];
  source_id?: string;
}

/** A named filter query. The query grammar lives in internal/taskfilter and is
 * resolved server-side (GET /api/views/{name} returns the matching tasks), so
 * the GUI never has to reimplement it. */
export interface SavedView {
  id: number;
  name: string;
  query: string;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface SavedViewResult {
  name: string;
  query: string;
  task_count: number;
  tasks: Task[];
}
