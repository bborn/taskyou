import type { Task, Project, TaskLog, TaskStatus } from '$lib/types';

// D1 database operations for the host (user accounts, sessions)
// Each user's task data lives in their sandbox's SQLite

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
				sandbox_id TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS tasks (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id TEXT NOT NULL,
				title TEXT NOT NULL,
				body TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'backlog',
				type TEXT NOT NULL DEFAULT 'code',
				project TEXT NOT NULL DEFAULT 'personal',
				worktree_path TEXT,
				branch_name TEXT,
				port INTEGER,
				pr_url TEXT,
				pr_number INTEGER,
				dangerous_mode INTEGER NOT NULL DEFAULT 0,
				scheduled_at TEXT,
				recurrence TEXT,
				last_run_at TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				started_at TEXT,
				completed_at TEXT,
				FOREIGN KEY (user_id) REFERENCES users(id)
			)
		`),
		db.prepare(`
			CREATE TABLE IF NOT EXISTS projects (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id TEXT NOT NULL,
				name TEXT NOT NULL,
				path TEXT NOT NULL DEFAULT '',
				aliases TEXT NOT NULL DEFAULT '',
				instructions TEXT NOT NULL DEFAULT '',
				color TEXT NOT NULL DEFAULT '#888888',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (user_id) REFERENCES users(id)
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
			CREATE TABLE IF NOT EXISTS settings (
				user_id TEXT NOT NULL,
				key TEXT NOT NULL,
				value TEXT NOT NULL DEFAULT '',
				PRIMARY KEY (user_id, key),
				FOREIGN KEY (user_id) REFERENCES users(id)
			)
		`),
	]);
}

// User operations
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
			.prepare('UPDATE users SET name = ?, avatar_url = ?, updated_at = datetime(\'now\') WHERE id = ?')
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

// Task operations
export async function listTasks(
	db: D1Database,
	userId: string,
	options: { status?: string; project?: string; type?: string; includeClosed?: boolean } = {},
): Promise<Task[]> {
	let query = 'SELECT * FROM tasks WHERE user_id = ?';
	const params: (string | number)[] = [userId];

	if (options.status) {
		query += ' AND status = ?';
		params.push(options.status);
	} else if (!options.includeClosed) {
		query += ' AND status != ?';
		params.push('done');
	}

	if (options.project) {
		query += ' AND project = ?';
		params.push(options.project);
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
	data: { title: string; body?: string; type?: string; project?: string },
): Promise<Task> {
	const result = await db
		.prepare(
			'INSERT INTO tasks (user_id, title, body, type, project) VALUES (?, ?, ?, ?, ?) RETURNING *',
		)
		.bind(userId, data.title, data.body || '', data.type || 'code', data.project || 'personal')
		.first<Task & { user_id: string; dangerous_mode: number }>();

	return rowToTask(result!);
}

export async function updateTask(
	db: D1Database,
	userId: string,
	taskId: number,
	data: { title?: string; body?: string; status?: TaskStatus; type?: string; project?: string },
): Promise<Task | null> {
	const sets: string[] = [];
	const params: (string | number)[] = [];

	if (data.title !== undefined) { sets.push('title = ?'); params.push(data.title); }
	if (data.body !== undefined) { sets.push('body = ?'); params.push(data.body); }
	if (data.status !== undefined) { sets.push('status = ?'); params.push(data.status); }
	if (data.type !== undefined) { sets.push('type = ?'); params.push(data.type); }
	if (data.project !== undefined) { sets.push('project = ?'); params.push(data.project); }

	if (sets.length === 0) return getTask(db, userId, taskId);

	sets.push("updated_at = datetime('now')");

	// Track status transitions
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

// Project operations
export async function listProjects(db: D1Database, userId: string): Promise<Project[]> {
	const result = await db
		.prepare('SELECT * FROM projects WHERE user_id = ? ORDER BY name')
		.bind(userId)
		.all<Project & { user_id: string }>();
	return (result.results || []).map(({ user_id, ...p }) => p as Project);
}

export async function createProject(
	db: D1Database,
	userId: string,
	data: { name: string; path: string; aliases?: string; instructions?: string; color?: string },
): Promise<Project> {
	const result = await db
		.prepare(
			'INSERT INTO projects (user_id, name, path, aliases, instructions, color) VALUES (?, ?, ?, ?, ?, ?) RETURNING *',
		)
		.bind(userId, data.name, data.path, data.aliases || '', data.instructions || '', data.color || '#888888')
		.first<Project & { user_id: string }>();

	const { user_id, ...project } = result!;
	return project as Project;
}

export async function updateProject(
	db: D1Database,
	userId: string,
	projectId: number,
	data: { name?: string; path?: string; aliases?: string; instructions?: string; color?: string },
): Promise<Project | null> {
	const sets: string[] = [];
	const params: (string | number)[] = [];

	if (data.name !== undefined) { sets.push('name = ?'); params.push(data.name); }
	if (data.path !== undefined) { sets.push('path = ?'); params.push(data.path); }
	if (data.aliases !== undefined) { sets.push('aliases = ?'); params.push(data.aliases); }
	if (data.instructions !== undefined) { sets.push('instructions = ?'); params.push(data.instructions); }
	if (data.color !== undefined) { sets.push('color = ?'); params.push(data.color); }

	if (sets.length === 0) return null;

	params.push(projectId, userId);

	const row = await db
		.prepare(`UPDATE projects SET ${sets.join(', ')} WHERE id = ? AND user_id = ? RETURNING *`)
		.bind(...params)
		.first<Project & { user_id: string }>();

	if (!row) return null;
	const { user_id, ...project } = row;
	return project as Project;
}

export async function deleteProject(db: D1Database, userId: string, projectId: number): Promise<boolean> {
	const result = await db
		.prepare('DELETE FROM projects WHERE id = ? AND user_id = ?')
		.bind(projectId, userId)
		.run();
	return (result.meta?.changes ?? 0) > 0;
}

// Task logs
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

// Settings
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

// Helper to convert DB row to Task
function rowToTask(row: Task & { user_id?: string; dangerous_mode: number | boolean }): Task {
	const { user_id, ...task } = row as Task & { user_id?: string };
	return {
		...task,
		dangerous_mode: Boolean(task.dangerous_mode),
	};
}
