// User and authentication types
export interface User {
	id: string;
	email: string;
	name: string;
	avatar_url: string;
	sandbox_id?: string;
	sandbox_status?: SandboxStatus;
}

export type SandboxStatus = 'pending' | 'creating' | 'running' | 'stopped' | 'error';

// Task types
export type TaskStatus = 'backlog' | 'queued' | 'processing' | 'blocked' | 'done';

export interface Task {
	id: number;
	title: string;
	body: string;
	status: TaskStatus;
	type: string;
	project: string;
	worktree_path?: string;
	branch_name?: string;
	port?: number;
	pr_url?: string;
	pr_number?: number;
	dangerous_mode: boolean;
	scheduled_at?: string;
	recurrence?: string;
	last_run_at?: string;
	created_at: string;
	updated_at: string;
	started_at?: string;
	completed_at?: string;
}

export interface CreateTaskRequest {
	title: string;
	body?: string;
	type?: string;
	project?: string;
}

export interface UpdateTaskRequest {
	title?: string;
	body?: string;
	status?: TaskStatus;
	type?: string;
	project?: string;
}

// Project types
export interface Project {
	id: number;
	name: string;
	path: string;
	aliases: string;
	instructions: string;
	color: string;
	created_at: string;
}

export interface CreateProjectRequest {
	name: string;
	path: string;
	aliases?: string;
	instructions?: string;
	color?: string;
}

export interface UpdateProjectRequest {
	name?: string;
	path?: string;
	aliases?: string;
	instructions?: string;
	color?: string;
}

// Task log types
export interface TaskLog {
	id: number;
	task_id: number;
	line_type: 'system' | 'text' | 'tool' | 'error' | 'output';
	content: string;
	created_at: string;
}

// WebSocket message types
export type WebSocketMessage =
	| { type: 'task_update'; data: Task }
	| { type: 'task_deleted'; data: { id: number } }
	| { type: 'task_log'; data: { task_id: number; log: TaskLog } };
