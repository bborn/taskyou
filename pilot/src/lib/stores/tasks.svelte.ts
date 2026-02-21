import type { Task, CreateTaskRequest, UpdateTaskRequest } from '$lib/types';
import { tasks as tasksApi } from '$lib/api/client';
import { navState } from './nav.svelte';

export const taskState = $state<{ tasks: Task[]; loading: boolean }>({
	tasks: [],
	loading: true,
});

// Derived getters for board columns
function byUpdatedDesc(a: Task, b: Task) {
	return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime();
}

function filterByProject(tasks: Task[]): Task[] {
	const pid = navState.activeProjectId;
	if (!pid) return tasks;
	return tasks.filter(t => t.project_id === pid);
}

export function getBacklogTasks(): Task[] {
	return filterByProject(taskState.tasks.filter((t) => t.status === 'backlog')).sort(byUpdatedDesc);
}

export function getInProgressTasks(): Task[] {
	return filterByProject(taskState.tasks
		.filter((t) => t.status === 'queued' || t.status === 'processing'))
		.sort((a, b) => {
			if (a.status === 'processing' && b.status !== 'processing') return -1;
			if (b.status === 'processing' && a.status !== 'processing') return 1;
			return byUpdatedDesc(a, b);
		});
}

export function getBlockedTasks(): Task[] {
	return filterByProject(taskState.tasks.filter((t) => t.status === 'blocked')).sort(byUpdatedDesc);
}

export function getDoneTasks(): Task[] {
	return filterByProject(taskState.tasks.filter((t) => t.status === 'done')).sort(byUpdatedDesc);
}

export function getFailedTasks(): Task[] {
	return filterByProject(taskState.tasks.filter((t) => t.status === 'failed')).sort(byUpdatedDesc);
}

export async function fetchTasks() {
	taskState.loading = true;
	try {
		const data = await tasksApi.list({ all: true });
		taskState.tasks = data;
	} catch (e) {
		console.error('Failed to fetch tasks:', e);
	} finally {
		taskState.loading = false;
	}
}

export async function createTask(data: CreateTaskRequest): Promise<Task> {
	const task = await tasksApi.create(data);
	taskState.tasks = [...taskState.tasks, task];
	return task;
}

export async function updateTask(id: number, data: UpdateTaskRequest): Promise<Task> {
	const task = await tasksApi.update(id, data);
	taskState.tasks = taskState.tasks.map((t) => (t.id === id ? task : t));
	return task;
}

export async function deleteTask(id: number): Promise<void> {
	await tasksApi.delete(id);
	taskState.tasks = taskState.tasks.filter((t) => t.id !== id);
}

// Periodic refresh (fallback when agent WebSocket is not connected)
let pollInterval: ReturnType<typeof setInterval> | null = null;

export function startPolling(interval = 10000) {
	stopPolling();
	pollInterval = setInterval(fetchTasks, interval);
}

export function stopPolling() {
	if (pollInterval) {
		clearInterval(pollInterval);
		pollInterval = null;
	}
}
