<script lang="ts">
	import { onMount } from 'svelte';
	import Header from './Header.svelte';
	import TaskBoard from './TaskBoard.svelte';
	import TaskDetail from './TaskDetail.svelte';
	import NewTaskDialog from './NewTaskDialog.svelte';
	import CommandPalette from './CommandPalette.svelte';
	import { taskState, fetchTasks, queueTask, closeTask, createTask } from '$lib/stores/tasks.svelte';
	import { projects as projectsApi } from '$lib/api/client';
	import type { User, Task, Project } from '$lib/types';

	interface Props {
		user: User;
		onLogout: () => void;
		onSettings: () => void;
	}

	let { user, onLogout, onSettings }: Props = $props();

	let projects = $state<Project[]>([]);
	let showNewTask = $state(false);
	let selectedTask = $state<Task | null>(null);
	let showCommandPalette = $state(false);

	onMount(async () => {
		try {
			projects = await projectsApi.list();
		} catch (e) {
			console.error(e);
		}
	});

	function handleKeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement;
		if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) return;

		if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
			e.preventDefault();
			showCommandPalette = !showCommandPalette;
			return;
		}

		if (e.key === 'n' && !e.metaKey && !e.ctrlKey) {
			e.preventDefault();
			showNewTask = true;
			return;
		}

		if (e.key === 'r' && !e.metaKey && !e.ctrlKey) {
			e.preventDefault();
			fetchTasks();
			return;
		}

		if (e.key === 's' && !e.metaKey && !e.ctrlKey && !showNewTask && !selectedTask) {
			e.preventDefault();
			onSettings();
			return;
		}

		if (e.key === 'Escape') {
			if (showCommandPalette) showCommandPalette = false;
			else if (showNewTask) showNewTask = false;
			else if (selectedTask) selectedTask = null;
		}
	}

	async function handleCreateTask(data: Parameters<typeof createTask>[0]) {
		await createTask(data);
	}

	function handleTaskClick(task: Task) {
		selectedTask = task;
	}

	function handleTaskUpdate(updatedTask: Task) {
		selectedTask = updatedTask;
	}

	async function handleQueue(id: number) {
		await queueTask(id);
	}

	async function handleRetry(id: number) {
		const task = taskState.tasks.find((t) => t.id === id);
		if (task) selectedTask = task;
	}

	async function handleClose(id: number) {
		await closeTask(id);
	}
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="min-h-screen bg-background">
	<Header
		{user}
		{onLogout}
		{onSettings}
		onCommandPalette={() => (showCommandPalette = true)}
	/>

	<main class="container mx-auto p-4 md:p-6">
		<TaskBoard
			onQueue={handleQueue}
			onRetry={handleRetry}
			onClose={handleClose}
			onTaskClick={handleTaskClick}
			onNewTask={() => (showNewTask = true)}
		/>
	</main>

	<!-- Keyboard shortcuts hint -->
	<div class="fixed bottom-4 right-4 hidden lg:flex items-center gap-4 text-xs text-muted-foreground">
		<span class="flex items-center gap-1">
			<kbd class="px-1.5 py-0.5 rounded bg-muted border border-border">N</kbd>
			new task
		</span>
		<span class="flex items-center gap-1">
			<kbd class="px-1.5 py-0.5 rounded bg-muted border border-border">R</kbd>
			refresh
		</span>
		<span class="flex items-center gap-1">
			<kbd class="px-1.5 py-0.5 rounded bg-muted border border-border">S</kbd>
			settings
		</span>
	</div>

	<CommandPalette
		isOpen={showCommandPalette}
		onClose={() => (showCommandPalette = false)}
		tasks={taskState.tasks}
		onSelectTask={(task) => (selectedTask = task)}
		onNewTask={() => (showNewTask = true)}
		{onSettings}
	/>

	{#if showNewTask}
		<NewTaskDialog
			{projects}
			onSubmit={handleCreateTask}
			onClose={() => (showNewTask = false)}
		/>
	{/if}

	{#if selectedTask}
		<TaskDetail
			task={selectedTask}
			onClose={() => (selectedTask = null)}
			onUpdate={handleTaskUpdate}
			onDelete={() => (selectedTask = null)}
		/>
	{/if}
</div>
