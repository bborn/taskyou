import type { Task, Chat, Message, Workspace, TaskStatus, TaskLog, Project, Model, Integration } from '$lib/types';

export async function initHostDB(db: D1Database): Promise<void> {
	await db.batch([
		db.prepare(`
			CREATE TABLE IF NOT EXISTS users (
				id TEXT PRIMARY KEY,
				email TEXT UNIQUE NOT NULL,
				name TEXT NOT NULL DEFAULT '',
				avatar_url TEXT NOT NULL DEFAULT '',
				provider TEXT NOT NULL,
				provider_id TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS workspaces (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				owner_id TEXT NOT NULL,
				autonomous_enabled INTEGER NOT NULL DEFAULT 0,
				weekly_budget_cents INTEGER NOT NULL DEFAULT 10000,
				budget_spent_cents INTEGER NOT NULL DEFAULT 0,
				polling_interval INTEGER NOT NULL DEFAULT 30,
				brand_voice TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (owner_id) REFERENCES users(id)
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS memberships (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id TEXT NOT NULL,
				workspace_id TEXT NOT NULL,
				role TEXT NOT NULL DEFAULT 'member',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				UNIQUE(user_id, workspace_id),
				FOREIGN KEY (user_id) REFERENCES users(id),
				FOREIGN KEY (workspace_id) REFERENCES workspaces(id)
			)
		`),
		// Projects — organizational containers for tasks
		db.prepare(`
			CREATE TABLE IF NOT EXISTS projects (
				id TEXT PRIMARY KEY,
				workspace_id TEXT NOT NULL DEFAULT 'default',
				user_id TEXT NOT NULL DEFAULT 'dev-user',
				name TEXT NOT NULL DEFAULT 'Default',
				instructions TEXT NOT NULL DEFAULT '',
				color TEXT NOT NULL DEFAULT '#888888',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (user_id) REFERENCES users(id)
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS tasks (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				workspace_id TEXT NOT NULL DEFAULT 'default',
				user_id TEXT NOT NULL,
				title TEXT NOT NULL,
				body TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'backlog',
				type TEXT NOT NULL DEFAULT 'code',
				project_id TEXT,
				chat_id TEXT,
				parent_task_id INTEGER,
				subtasks_json TEXT,
				cost_cents INTEGER NOT NULL DEFAULT 0,
				output TEXT,
				summary TEXT,
				approval_status TEXT,
				dangerous_mode INTEGER NOT NULL DEFAULT 0,
				scheduled_at TEXT,
				recurrence TEXT,
				last_run_at TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				started_at TEXT,
				completed_at TEXT,
				FOREIGN KEY (user_id) REFERENCES users(id),
				FOREIGN KEY (parent_task_id) REFERENCES tasks(id),
				FOREIGN KEY (project_id) REFERENCES projects(id),
				FOREIGN KEY (chat_id) REFERENCES chats(id)
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS task_logs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				task_id INTEGER NOT NULL,
				line_type TEXT NOT NULL DEFAULT 'text',
				content TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS chats (
				id TEXT PRIMARY KEY,
				workspace_id TEXT NOT NULL DEFAULT 'default',
				user_id TEXT NOT NULL,
				project_id TEXT,
				title TEXT NOT NULL DEFAULT 'New Chat',
				model_id TEXT NOT NULL DEFAULT 'claude-sonnet-4-5-20250929',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (user_id) REFERENCES users(id),
				FOREIGN KEY (project_id) REFERENCES projects(id)
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS messages (
				id TEXT PRIMARY KEY,
				chat_id TEXT NOT NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				model_id TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (chat_id) REFERENCES chats(id) ON DELETE CASCADE
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS integrations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				workspace_id TEXT NOT NULL DEFAULT 'default',
				provider TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'disconnected',
				external_id TEXT,
				access_token_encrypted TEXT,
				refresh_token_encrypted TEXT,
				token_expires_at TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS models (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				provider TEXT NOT NULL,
				api_id TEXT NOT NULL,
				context_window INTEGER NOT NULL DEFAULT 200000,
				input_price_per_million REAL NOT NULL DEFAULT 0,
				output_price_per_million REAL NOT NULL DEFAULT 0,
				supports_tools INTEGER NOT NULL DEFAULT 1,
				supports_streaming INTEGER NOT NULL DEFAULT 1
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS settings (
				user_id TEXT NOT NULL,
				key TEXT NOT NULL,
				value TEXT NOT NULL DEFAULT '',
				PRIMARY KEY (user_id, key),
				FOREIGN KEY (user_id) REFERENCES users(id)
			)
		`),
	]);

	// Seed default models
	await db.prepare(`INSERT OR IGNORE INTO models (id, name, provider, api_id, context_window, input_price_per_million, output_price_per_million) VALUES
		('claude-sonnet-4-5-20250929', 'Claude Sonnet 4.5', 'anthropic', 'claude-sonnet-4-5-20250929', 200000, 3, 15),
		('claude-haiku-4-5-20251001', 'Claude Haiku 4.5', 'anthropic', 'claude-haiku-4-5-20251001', 200000, 0.8, 4),
		('claude-opus-4-6', 'Claude Opus 4.6', 'anthropic', 'claude-opus-4-6', 200000, 15, 75)
	`).run();

	// Seed default workspace for dev
	await db.prepare(`INSERT OR IGNORE INTO workspaces (id, name, owner_id) VALUES ('default', 'Personal', 'dev-user')`).run();
}

// ── User operations ──

export async function findOrCreateUser(
	db: D1Database,
	provider: string,
	providerId: string,
	email: string,
	name: string,
	avatarUrl: string,
): Promise<{ id: string; email: string; name: string; avatar_url: string }> {
	const id = `${provider}:${providerId}`;
	const existing = await db
		.prepare('SELECT id, email, name, avatar_url FROM users WHERE id = ?')
		.bind(id)
		.first<{ id: string; email: string; name: string; avatar_url: string }>();

	if (existing) {
		await db
			.prepare("UPDATE users SET name = ?, avatar_url = ?, updated_at = datetime('now') WHERE id = ?")
			.bind(name, avatarUrl, id)
			.run();
		return { ...existing, name, avatar_url: avatarUrl };
	}

	await db
		.prepare('INSERT INTO users (id, email, name, avatar_url, provider, provider_id) VALUES (?, ?, ?, ?, ?, ?)')
		.bind(id, email, name, avatarUrl, provider, providerId)
		.run();

	return { id, email, name, avatar_url: avatarUrl };
}

export async function getUserById(
	db: D1Database,
	userId: string,
): Promise<{ id: string; email: string; name: string; avatar_url: string } | null> {
	return db
		.prepare('SELECT id, email, name, avatar_url FROM users WHERE id = ?')
		.bind(userId)
		.first();
}

// ── Task operations ──

export async function listTasks(
	db: D1Database,
	userId: string,
	options: { status?: string; project_id?: string; type?: string; includeClosed?: boolean } = {},
): Promise<Task[]> {
	let query = 'SELECT * FROM tasks WHERE user_id = ?';
	const params: (string | number)[] = [userId];

	if (options.status) {
		query += ' AND status = ?';
		params.push(options.status);
	} else if (!options.includeClosed) {
		query += " AND status NOT IN ('done', 'failed')";
	}

	if (options.project_id) {
		query += ' AND project_id = ?';
		params.push(options.project_id);
	}

	if (options.type) {
		query += ' AND type = ?';
		params.push(options.type);
	}

	query += ' ORDER BY updated_at DESC';

	const result = await db.prepare(query).bind(...params).all<Task & { user_id: string; dangerous_mode: number }>();
	return (result.results || []).map(rowToTask);
}

export async function getTask(db: D1Database, userId: string, taskId: number): Promise<Task | null> {
	const row = await db
		.prepare('SELECT * FROM tasks WHERE id = ? AND user_id = ?')
		.bind(taskId, userId)
		.first<Task & { user_id: string; dangerous_mode: number }>();
	return row ? rowToTask(row) : null;
}

export async function createTask(
	db: D1Database,
	userId: string,
	data: { title: string; body?: string; type?: string; project_id?: string; chat_id?: string },
): Promise<Task> {
	const result = await db
		.prepare(
			'INSERT INTO tasks (user_id, title, body, type, project_id, chat_id) VALUES (?, ?, ?, ?, ?, ?) RETURNING *',
		)
		.bind(userId, data.title, data.body || '', data.type || 'code', data.project_id || null, data.chat_id || null)
		.first<Task & { user_id: string; dangerous_mode: number }>();

	return rowToTask(result!);
}

export async function updateTask(
	db: D1Database,
	userId: string,
	taskId: number,
	data: { title?: string; body?: string; status?: TaskStatus; type?: string; project_id?: string; output?: string; summary?: string },
): Promise<Task | null> {
	const sets: string[] = [];
	const params: (string | number)[] = [];

	if (data.title !== undefined) { sets.push('title = ?'); params.push(data.title); }
	if (data.body !== undefined) { sets.push('body = ?'); params.push(data.body); }
	if (data.status !== undefined) { sets.push('status = ?'); params.push(data.status); }
	if (data.type !== undefined) { sets.push('type = ?'); params.push(data.type); }
	if (data.project_id !== undefined) { sets.push('project_id = ?'); params.push(data.project_id); }
	if (data.output !== undefined) { sets.push('output = ?'); params.push(data.output); }
	if (data.summary !== undefined) { sets.push('summary = ?'); params.push(data.summary); }

	if (sets.length === 0) return getTask(db, userId, taskId);

	sets.push("updated_at = datetime('now')");

	if (data.status === 'processing' || data.status === 'queued') {
		sets.push("started_at = COALESCE(started_at, datetime('now'))");
	}
	if (data.status === 'done') {
		sets.push("completed_at = datetime('now')");
	}

	params.push(taskId, userId);

	const row = await db
		.prepare(`UPDATE tasks SET ${sets.join(', ')} WHERE id = ? AND user_id = ? RETURNING *`)
		.bind(...params)
		.first<Task & { user_id: string; dangerous_mode: number }>();

	return row ? rowToTask(row) : null;
}

export async function deleteTask(db: D1Database, userId: string, taskId: number): Promise<boolean> {
	const result = await db
		.prepare('DELETE FROM tasks WHERE id = ? AND user_id = ?')
		.bind(taskId, userId)
		.run();
	return (result.meta?.changes ?? 0) > 0;
}

// ── Chat operations ──

export async function listChats(db: D1Database, userId: string): Promise<Chat[]> {
	const result = await db
		.prepare('SELECT * FROM chats WHERE user_id = ? ORDER BY updated_at DESC')
		.bind(userId)
		.all<Chat>();
	return result.results || [];
}

export async function getChat(db: D1Database, userId: string, chatId: string): Promise<Chat | null> {
	return db
		.prepare('SELECT * FROM chats WHERE id = ? AND user_id = ?')
		.bind(chatId, userId)
		.first<Chat>();
}

export async function createChat(
	db: D1Database,
	userId: string,
	data: { title?: string; model_id?: string; project_id?: string },
): Promise<Chat> {
	const id = crypto.randomUUID();
	const result = await db
		.prepare('INSERT INTO chats (id, user_id, title, model_id, project_id) VALUES (?, ?, ?, ?, ?) RETURNING *')
		.bind(id, userId, data.title || 'New Chat', data.model_id || 'claude-sonnet-4-5-20250929', data.project_id || null)
		.first<Chat>();
	return result!;
}

export async function updateChat(
	db: D1Database,
	userId: string,
	chatId: string,
	data: { title?: string; model_id?: string },
): Promise<Chat | null> {
	const sets: string[] = [];
	const params: (string)[] = [];

	if (data.title !== undefined) { sets.push('title = ?'); params.push(data.title); }
	if (data.model_id !== undefined) { sets.push('model_id = ?'); params.push(data.model_id); }

	if (sets.length === 0) return getChat(db, userId, chatId);

	sets.push("updated_at = datetime('now')");
	params.push(chatId, userId);

	const row = await db
		.prepare(`UPDATE chats SET ${sets.join(', ')} WHERE id = ? AND user_id = ? RETURNING *`)
		.bind(...params)
		.first<Chat>();

	return row;
}

export async function deleteChat(db: D1Database, userId: string, chatId: string): Promise<boolean> {
	const result = await db
		.prepare('DELETE FROM chats WHERE id = ? AND user_id = ?')
		.bind(chatId, userId)
		.run();
	return (result.meta?.changes ?? 0) > 0;
}

// ── Message operations ──

export async function listMessages(db: D1Database, chatId: string): Promise<Message[]> {
	const result = await db
		.prepare('SELECT * FROM messages WHERE chat_id = ? ORDER BY created_at ASC')
		.bind(chatId)
		.all<Message>();
	return result.results || [];
}

export async function createMessage(
	db: D1Database,
	data: { chat_id: string; role: string; content: string; model_id?: string; input_tokens?: number; output_tokens?: number },
): Promise<Message> {
	const id = crypto.randomUUID();
	const result = await db
		.prepare('INSERT INTO messages (id, chat_id, role, content, model_id, input_tokens, output_tokens) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING *')
		.bind(id, data.chat_id, data.role, data.content, data.model_id || null, data.input_tokens || 0, data.output_tokens || 0)
		.first<Message>();
	return result!;
}

// ── Model operations ──

export async function listModels(db: D1Database): Promise<Model[]> {
	const result = await db.prepare('SELECT * FROM models ORDER BY name').all<Model>();
	return (result.results || []).map(m => ({
		...m,
		supports_tools: Boolean(m.supports_tools),
		supports_streaming: Boolean(m.supports_streaming),
	}));
}

// ── Workspace operations ──

export async function listWorkspaces(db: D1Database, userId: string): Promise<Workspace[]> {
	const result = await db
		.prepare(
			`SELECT w.* FROM workspaces w
			 LEFT JOIN memberships m ON m.workspace_id = w.id AND m.user_id = ?
			 WHERE w.owner_id = ? OR m.user_id = ?
			 ORDER BY w.name`,
		)
		.bind(userId, userId, userId)
		.all<Workspace>();
	return result.results || [];
}

export async function getWorkspace(db: D1Database, id: string): Promise<Workspace | null> {
	return db.prepare('SELECT * FROM workspaces WHERE id = ?').bind(id).first<Workspace>();
}

export async function createWorkspace(
	db: D1Database,
	ownerId: string,
	data: { name: string },
): Promise<Workspace> {
	const id = crypto.randomUUID();
	const result = await db
		.prepare('INSERT INTO workspaces (id, name, owner_id) VALUES (?, ?, ?) RETURNING *')
		.bind(id, data.name, ownerId)
		.first<Workspace>();
	await db
		.prepare('INSERT INTO memberships (user_id, workspace_id, role) VALUES (?, ?, ?)')
		.bind(ownerId, id, 'owner')
		.run();
	return result!;
}

export async function updateWorkspace(
	db: D1Database,
	id: string,
	data: { name?: string; autonomous_enabled?: boolean; weekly_budget_cents?: number },
): Promise<Workspace | null> {
	const sets: string[] = [];
	const params: (string | number)[] = [];

	if (data.name !== undefined) { sets.push('name = ?'); params.push(data.name); }
	if (data.autonomous_enabled !== undefined) { sets.push('autonomous_enabled = ?'); params.push(data.autonomous_enabled ? 1 : 0); }
	if (data.weekly_budget_cents !== undefined) { sets.push('weekly_budget_cents = ?'); params.push(data.weekly_budget_cents); }

	if (sets.length === 0) return null;

	sets.push("updated_at = datetime('now')");
	params.push(id);

	return db
		.prepare(`UPDATE workspaces SET ${sets.join(', ')} WHERE id = ? RETURNING *`)
		.bind(...params)
		.first<Workspace>();
}

export async function deleteWorkspace(db: D1Database, id: string): Promise<boolean> {
	const result = await db.prepare('DELETE FROM workspaces WHERE id = ?').bind(id).run();
	return (result.meta?.changes ?? 0) > 0;
}

// ── Integration operations ──

export async function listIntegrations(db: D1Database): Promise<Integration[]> {
	const result = await db
		.prepare('SELECT id, workspace_id, provider, status, external_id, created_at, updated_at FROM integrations ORDER BY provider')
		.all<Integration>();
	return result.results || [];
}

// ── Project operations ──

export async function listProjects(db: D1Database, userId: string): Promise<Project[]> {
	const result = await db
		.prepare('SELECT * FROM projects WHERE user_id = ? ORDER BY created_at DESC')
		.bind(userId)
		.all<Project>();
	return result.results || [];
}

export async function createProject(
	db: D1Database,
	userId: string,
	data: { name?: string; instructions?: string; color?: string },
): Promise<Project> {
	const id = crypto.randomUUID();
	const result = await db
		.prepare('INSERT INTO projects (id, user_id, name, instructions, color) VALUES (?, ?, ?, ?, ?) RETURNING *')
		.bind(id, userId, data.name || 'Default', data.instructions || '', data.color || '#888888')
		.first<Project>();
	return result!;
}

export async function getProjectById(
	db: D1Database,
	projectId: string,
): Promise<Project | null> {
	return db
		.prepare('SELECT * FROM projects WHERE id = ?')
		.bind(projectId)
		.first<Project>();
}

export async function updateProject(
	db: D1Database,
	projectId: string,
	data: { name?: string; instructions?: string; color?: string },
): Promise<Project | null> {
	const sets: string[] = [];
	const params: (string | number)[] = [];

	if (data.name !== undefined) { sets.push('name = ?'); params.push(data.name); }
	if (data.instructions !== undefined) { sets.push('instructions = ?'); params.push(data.instructions); }
	if (data.color !== undefined) { sets.push('color = ?'); params.push(data.color); }

	if (sets.length === 0) return getProjectById(db, projectId);

	sets.push("updated_at = datetime('now')");
	params.push(projectId);

	return db
		.prepare(`UPDATE projects SET ${sets.join(', ')} WHERE id = ? RETURNING *`)
		.bind(...params)
		.first<Project>();
}

export async function deleteProject(db: D1Database, projectId: string): Promise<boolean> {
	const result = await db
		.prepare('DELETE FROM projects WHERE id = ?')
		.bind(projectId)
		.run();
	return (result.meta?.changes ?? 0) > 0;
}

// ── Task logs ──

export async function getTaskLogs(
	db: D1Database,
	userId: string,
	taskId: number,
	limit = 200,
): Promise<TaskLog[]> {
	const result = await db
		.prepare(
			`SELECT tl.* FROM task_logs tl
			 JOIN tasks t ON t.id = tl.task_id
			 WHERE tl.task_id = ? AND t.user_id = ?
			 ORDER BY tl.id DESC LIMIT ?`,
		)
		.bind(taskId, userId, limit)
		.all<TaskLog>();
	return result.results || [];
}

export async function addTaskLog(
	db: D1Database,
	taskId: number,
	lineType: string,
	content: string,
): Promise<TaskLog> {
	const result = await db
		.prepare(
			'INSERT INTO task_logs (task_id, line_type, content) VALUES (?, ?, ?) RETURNING *',
		)
		.bind(taskId, lineType, content)
		.first<TaskLog>();
	return result!;
}

// ── Settings ──

export async function getSettings(db: D1Database, userId: string): Promise<Record<string, string>> {
	const result = await db
		.prepare('SELECT key, value FROM settings WHERE user_id = ?')
		.bind(userId)
		.all<{ key: string; value: string }>();

	const settings: Record<string, string> = {};
	for (const row of result.results || []) {
		settings[row.key] = row.value;
	}
	return settings;
}

export async function updateSettings(
	db: D1Database,
	userId: string,
	data: Record<string, string>,
): Promise<void> {
	const stmts = Object.entries(data).map(([key, value]) =>
		db
			.prepare('INSERT OR REPLACE INTO settings (user_id, key, value) VALUES (?, ?, ?)')
			.bind(userId, key, value),
	);
	if (stmts.length > 0) {
		await db.batch(stmts);
	}
}

// ── Helpers ──

function rowToTask(row: Task & { user_id?: string; dangerous_mode: number | boolean }): Task {
	const { user_id, ...task } = row as Task & { user_id?: string };
	return {
		...task,
		dangerous_mode: Boolean(task.dangerous_mode),
	};
}
