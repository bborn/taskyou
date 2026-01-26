import type {
  User,
  Sprite,
  Task,
  CreateTaskRequest,
  UpdateTaskRequest,
  Project,
  CreateProjectRequest,
  UpdateProjectRequest,
  TaskLog,
} from './types';

// In development, use the local API server. In production, use relative /api path.
const API_BASE = import.meta.env.DEV ? 'http://localhost:8081' : '/api';

async function fetchJSON<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
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

  // Handle 204 No Content
  if (response.status === 204) {
    return undefined as T;
  }

  return response.json();
}

// Auth API
export const auth = {
  getMe: () => fetchJSON<User>('/auth/me'),
  logout: () => fetchJSON<{ success: boolean }>('/auth/logout', { method: 'POST' }),
};

// Sprite API
export const sprite = {
  get: () => fetchJSON<Sprite | null>('/sprite'),
  create: () => fetchJSON<Sprite>('/sprite', { method: 'POST' }),
  start: () => fetchJSON<{ success: boolean }>('/sprite/start', { method: 'POST' }),
  stop: () => fetchJSON<{ success: boolean }>('/sprite/stop', { method: 'POST' }),
  destroy: () => fetchJSON<{ success: boolean }>('/sprite', { method: 'DELETE' }),
  getSSHConfig: async () => {
    const response = await fetch(`${API_BASE}/sprite/ssh-config`, {
      credentials: 'include',
    });
    if (!response.ok) {
      throw new Error('Failed to get SSH config');
    }
    return response.text();
  },
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
  getTerminal: (id: number) =>
    fetchJSON<TerminalInfo>(`/tasks/${id}/terminal`),
  stopTerminal: (id: number) =>
    fetchJSON<void>(`/tasks/${id}/terminal`, { method: 'DELETE' }),
};

// Terminal info returned by the API
export interface TerminalInfo {
  task_id: number;
  port: number;
  tmux_target: string;
  websocket_url: string;
}

// Projects API
export const projects = {
  list: () => fetchJSON<Project[]>('/projects'),
  get: (id: number) => fetchJSON<Project>(`/projects/${id}`),
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

// Export API
export const exportData = {
  download: async () => {
    const response = await fetch(`${API_BASE}/export`, {
      credentials: 'include',
    });
    if (!response.ok) {
      throw new Error('Failed to export data');
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'tasks-export.db';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  },
};
