import { writable, derived } from 'svelte/store';
import type { Task, CreateTaskRequest, UpdateTaskRequest, TaskStatus } from '$lib/types';
import { tasks as tasksApi } from '$lib/api/client';

export const tasks = writable<Task[]>([]);
export const tasksLoading = writable(true);

// Derived stores for board columns
export const backlogTasks = derived(tasks, ($tasks) =>
	$tasks.filter((t) => t.status === 'backlog').sort(byUpdatedDesc),
);
export const inProgressTasks = derived(tasks, ($tasks) =>
	$tasks
		.filter((t) => t.status === 'queued' || t.status === 'processing')
		.sort((a, b) => {
			if (a.status === 'processing' && b.status !== 'processing') return -1;
			if (b.status === 'processing' && a.status !== 'processing') return 1;
			return byUpdatedDesc(a, b);
		}),
);
export const blockedTasks = derived(tasks, ($tasks) =>
	$tasks.filter((t) => t.status === 'blocked').sort(byUpdatedDesc),
);
export const doneTasks = derived(tasks, ($tasks) =>
	$tasks.filter((t) => t.status === 'done').sort(byUpdatedDesc),
);

function byUpdatedDesc(a: Task, b: Task) {
	return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime();
}

export async function fetchTasks() {
	tasksLoading.set(true);
	try {
		const data = await tasksApi.list({ all: true });
		tasks.set(data);
	} catch (e) {
		console.error('Failed to fetch tasks:', e);
	} finally {
		tasksLoading.set(false);
	}
}

export async function createTask(data: CreateTaskRequest): Promise<Task> {
	const task = await tasksApi.create(data);
	tasks.update((prev) => [...prev, task]);
	return task;
}

export async function updateTask(id: number, data: UpdateTaskRequest): Promise<Task> {
	const task = await tasksApi.update(id, data);
	tasks.update((prev) => prev.map((t) => (t.id === id ? task : t)));
	return task;
}

export async function deleteTask(id: number): Promise<void> {
	await tasksApi.delete(id);
	tasks.update((prev) => prev.filter((t) => t.id !== id));
}

export async function queueTask(id: number): Promise<Task> {
	const task = await tasksApi.queue(id);
	tasks.update((prev) => prev.map((t) => (t.id === id ? task : t)));
	return task;
}

export async function retryTask(id: number, feedback?: string): Promise<Task> {
	const task = await tasksApi.retry(id, feedback);
	tasks.update((prev) => prev.map((t) => (t.id === id ? task : t)));
	return task;
}

export async function closeTask(id: number): Promise<Task> {
	const task = await tasksApi.close(id);
	tasks.update((prev) => prev.map((t) => (t.id === id ? task : t)));
	return task;
}

// Periodic refresh for active tasks
let pollInterval: ReturnType<typeof setInterval> | null = null;

export function startPolling() {
	stopPolling();
	pollInterval = setInterval(fetchTasks, 5000);
}

export function stopPolling() {
	if (pollInterval) {
		clearInterval(pollInterval);
		pollInterval = null;
	}
}
