import type {
	User,
	Task,
	CreateTaskRequest,
	UpdateTaskRequest,
	Project,
	CreateProjectRequest,
	UpdateProjectRequest,
	TaskLog,
	Chat,
	Message,
	Model,
	Integration,
	Workspace,
} from '$lib/types';

async function fetchJSON<T>(path: string, options?: RequestInit): Promise<T> {
	const response = await fetch(`/api${path}`, {
		...options,
		headers: {
			'Content-Type': 'application/json',
			...options?.headers,
		},
		credentials: 'include',
	});

	if (!response.ok) {
		const error = await response.json().catch(() => ({ error: response.statusText })) as { error?: string };
		throw new Error(error.error || 'Request failed');
	}

	if (response.status === 204) {
		return undefined as T;
	}

	return response.json();
}

// Auth API
export const auth = {
	getMe: () => fetchJSON<User>('/auth'),
	logout: () => fetchJSON<{ success: boolean }>('/auth', { method: 'POST' }),
};

// Tasks API
export const tasks = {
	list: (options?: { status?: string; project_id?: string; type?: string; all?: boolean }) => {
		const params = new URLSearchParams();
		if (options?.status) params.set('status', options.status);
		if (options?.project_id) params.set('project_id', options.project_id);
		if (options?.type) params.set('type', options.type);
		if (options?.all) params.set('all', 'true');
		const query = params.toString();
		return fetchJSON<Task[]>(`/tasks${query ? `?${query}` : ''}`);
	},
	get: (id: number) => fetchJSON<Task>(`/tasks/${id}`),
	create: (data: CreateTaskRequest) =>
		fetchJSON<Task>('/tasks', {
			method: 'POST',
			body: JSON.stringify(data),
		}),
	update: (id: number, data: UpdateTaskRequest) =>
		fetchJSON<Task>(`/tasks/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data),
		}),
	delete: (id: number) =>
		fetchJSON<void>(`/tasks/${id}`, { method: 'DELETE' }),
	getLogs: (id: number, limit?: number) => {
		const params = limit ? `?limit=${limit}` : '';
		return fetchJSON<TaskLog[]>(`/tasks/${id}/logs${params}`);
	},
};

// Projects API
export const projects = {
	list: () => fetchJSON<Project[]>('/projects'),
	get: (id: string) => fetchJSON<Project>(`/projects/${id}`),
	create: (data?: CreateProjectRequest) =>
		fetchJSON<Project>('/projects', {
			method: 'POST',
			body: JSON.stringify(data || {}),
		}),
	update: (id: string, data: UpdateProjectRequest) =>
		fetchJSON<Project>(`/projects/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data),
		}),
	delete: (id: string) =>
		fetchJSON<void>(`/projects/${id}`, { method: 'DELETE' }),
};

// Chats API
export const chats = {
	list: () => fetchJSON<Chat[]>('/chats'),
	get: (id: string) => fetchJSON<Chat>(`/chats/${id}`),
	create: (data?: { title?: string; model_id?: string; project_id?: string }) =>
		fetchJSON<Chat>('/chats', {
			method: 'POST',
			body: JSON.stringify(data || {}),
		}),
	update: (id: string, data: { title?: string; model_id?: string }) =>
		fetchJSON<Chat>(`/chats/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data),
		}),
	delete: (id: string) =>
		fetchJSON<void>(`/chats/${id}`, { method: 'DELETE' }),
};

// Messages API
export const messages = {
	list: (chatId: string) => fetchJSON<Message[]>(`/chats/${chatId}/messages`),
};

// Models API
export const models = {
	list: () => fetchJSON<Model[]>('/models'),
};

// Integrations API
export const integrations = {
	list: () => fetchJSON<Integration[]>('/integrations'),
};

// Workspaces API
export const workspaces = {
	list: () => fetchJSON<Workspace[]>('/workspaces'),
	get: (id: string) => fetchJSON<Workspace>(`/workspaces/${id}`),
	create: (data: { name: string }) =>
		fetchJSON<Workspace>('/workspaces', {
			method: 'POST',
			body: JSON.stringify(data),
		}),
	update: (id: string, data: { name?: string; autonomous_enabled?: boolean; weekly_budget_cents?: number }) =>
		fetchJSON<Workspace>(`/workspaces/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data),
		}),
	delete: (id: string) =>
		fetchJSON<void>(`/workspaces/${id}`, { method: 'DELETE' }),
};

// Settings API
export const settings = {
	get: () => fetchJSON<Record<string, string>>('/settings'),
	update: (data: Record<string, string>) =>
		fetchJSON<{ success: boolean }>('/settings', {
			method: 'PUT',
			body: JSON.stringify(data),
		}),
};
