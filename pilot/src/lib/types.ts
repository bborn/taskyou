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
	project: string;
	workspace_id: string;
	parent_task_id?: number;
	subtasks_json?: string;
	cost_cents: number;
	worktree_path?: string;
	branch_name?: string;
	port?: number;
	pr_url?: string;
	pr_number?: number;
	output?: string;
	summary?: string;
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

// Chat types
export interface Chat {
	id: string;
	workspace_id: string;
	user_id: string;
	title: string;
	model_id: string;
	sandbox_id?: string;
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

export interface ToolCall {
	id: string;
	message_id: string;
	tool_name: string;
	arguments_json: string;
	result_json?: string;
	status: 'pending' | 'running' | 'completed' | 'failed';
	created_at: string;
}

// Agent action types
export type RiskLevel = 'low' | 'medium' | 'high';
export type ActionStatus = 'completed' | 'pending_approval' | 'rejected' | 'failed';

export interface AgentAction {
	id: number;
	workspace_id: string;
	task_id?: number;
	sandbox_id?: string;
	action_type: string;
	description: string;
	reasoning?: string;
	risk_level: RiskLevel;
	cost_cents: number;
	status: ActionStatus;
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

// Sandbox types
export type SandboxStatus = 'pending' | 'provisioning' | 'running' | 'stopped' | 'error';

export interface Sandbox {
	id: string;
	workspace_id: string;
	name: string;
	status: SandboxStatus;
	provider: 'cloudflare' | 'local';
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

// Inbound message types
export interface InboundMessage {
	id: number;
	workspace_id: string;
	channel: 'email' | 'telegram' | 'webhook';
	sender: string;
	subject?: string;
	body: string;
	processed: boolean;
	chat_id?: string;
	created_at: string;
}

// Navigation
export type NavView = 'dashboard' | 'workspaces' | 'sandboxes' | 'integrations' | 'approvals' | 'settings';
