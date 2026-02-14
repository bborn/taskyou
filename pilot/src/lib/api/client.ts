import type {
	User,
	Task,
	CreateTaskRequest,
	UpdateTaskRequest,
	Project,
	CreateProjectRequest,
	UpdateProjectRequest,
	TaskLog,
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
		const error = await response.json().catch(() => ({ error: response.statusText }));
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
	list: (options?: { status?: string; project?: string; type?: string; all?: boolean }) => {
		const params = new URLSearchParams();
		if (options?.status) params.set('status', options.status);
		if (options?.project) params.set('project', options.project);
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
	queue: (id: number) =>
		fetchJSON<Task>(`/tasks/${id}/queue`, { method: 'POST' }),
	retry: (id: number, feedback?: string) =>
		fetchJSON<Task>(`/tasks/${id}/retry`, {
			method: 'POST',
			body: JSON.stringify({ feedback }),
		}),
	close: (id: number) =>
		fetchJSON<Task>(`/tasks/${id}/close`, { method: 'POST' }),
	getLogs: (id: number, limit?: number) => {
		const params = limit ? `?limit=${limit}` : '';
		return fetchJSON<TaskLog[]>(`/tasks/${id}/logs${params}`);
	},
};

// Projects API
export const projects = {
	list: () => fetchJSON<Project[]>('/projects'),
	create: (data: CreateProjectRequest) =>
		fetchJSON<Project>('/projects', {
			method: 'POST',
			body: JSON.stringify(data),
		}),
	update: (id: number, data: UpdateProjectRequest) =>
		fetchJSON<Project>(`/projects/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data),
		}),
	delete: (id: number) =>
		fetchJSON<void>(`/projects/${id}`, { method: 'DELETE' }),
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
