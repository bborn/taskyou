// User and authentication types
export interface User {
	id: string;
	email: string;
	name: string;
	avatar_url: string;
}

// Workspace types
export interface Workspace {
	id: string;
	name: string;
	owner_id: string;
	autonomous_enabled: boolean;
	weekly_budget_cents: number;
	budget_spent_cents: number;
	polling_interval: number;
	brand_voice: string;
	created_at: string;
	updated_at: string;
}

export interface Membership {
	id: number;
	user_id: string;
	workspace_id: string;
	role: 'owner' | 'admin' | 'member';
	created_at: string;
}

// Task types
export type TaskStatus = 'backlog' | 'queued' | 'processing' | 'blocked' | 'done' | 'failed';

export interface Task {
	id: number;
	title: string;
	body: string;
	status: TaskStatus;
	type: string;
	project_id: string;
	chat_id?: string;
	workspace_id: string;
	parent_task_id?: number;
	subtasks_json?: string;
	cost_cents: number;
	output?: string;
	summary?: string;
	preview_url?: string;
	approval_status?: 'pending_review' | 'approved' | 'rejected';
	dangerous_mode: boolean;
	scheduled_at?: string;
	recurrence?: string;
	last_run_at?: string;
	created_at: string;
	updated_at: string;
	started_at?: string;
	completed_at?: string;
}

export interface TaskFile {
	id: number;
	task_id: number;
	path: string;
	mime_type: string;
	size_bytes: number;
	created_at: string;
}

export interface CreateTaskRequest {
	title: string;
	body?: string;
	type?: string;
	project_id?: string;
	chat_id?: string;
}

export interface UpdateTaskRequest {
	title?: string;
	body?: string;
	status?: TaskStatus;
	type?: string;
	project_id?: string;
}

// Project types — organizational containers for tasks
export interface Project {
	id: string;
	workspace_id: string;
	user_id: string;
	name: string;
	instructions: string;
	color: string;
	github_repo?: string;
	github_branch?: string;
	created_at: string;
	updated_at: string;
}

export interface CreateProjectRequest {
	name: string;
	instructions?: string;
	color?: string;
	github_repo?: string;
	github_branch?: string;
}

export interface UpdateProjectRequest {
	name?: string;
	instructions?: string;
	color?: string;
	github_repo?: string;
	github_branch?: string;
}

// Chat types
export interface Chat {
	id: string;
	workspace_id: string;
	user_id: string;
	project_id?: string;
	title: string;
	model_id: string;
	created_at: string;
	updated_at: string;
}

export type MessageRole = 'system' | 'user' | 'assistant' | 'tool';

export interface Message {
	id: string;
	chat_id: string;
	role: MessageRole;
	content: string;
	input_tokens: number;
	output_tokens: number;
	model_id?: string;
	created_at: string;
}

// Integration types
export interface Integration {
	id: number;
	workspace_id: string;
	provider: 'github' | 'gmail' | 'slack' | 'linear';
	status: 'connected' | 'disconnected' | 'error';
	external_id?: string;
	created_at: string;
	updated_at: string;
}

// Model types
export interface Model {
	id: string;
	name: string;
	provider: string;
	api_id: string;
	context_window: number;
	input_price_per_million: number;
	output_price_per_million: number;
	supports_tools: boolean;
	supports_streaming: boolean;
}

// Task log types
export interface TaskLog {
	id: number;
	task_id: number;
	line_type: 'system' | 'text' | 'tool' | 'error' | 'output';
	content: string;
	created_at: string;
}

// Agent chat message (from AI SDK)
export interface AgentChatMessage {
	id: string;
	role: 'user' | 'assistant';
	content: string;
	createdAt?: string;
	toolInvocations?: ToolInvocation[];
}

export interface ToolInvocation {
	toolCallId: string;
	toolName: string;
	args: Record<string, unknown>;
	state: 'call' | 'result' | 'partial-call';
	result?: unknown;
}

// Navigation
export type NavView = 'dashboard' | 'workspaces' | 'integrations' | 'approvals' | 'settings';
