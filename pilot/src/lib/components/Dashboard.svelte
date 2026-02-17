<script lang="ts">
	import { onMount } from 'svelte';
	import { X } from 'lucide-svelte';
	import TaskBoard from './TaskBoard.svelte';
	import TaskDetail from './TaskDetail.svelte';
	import NewTaskDialog from './NewTaskDialog.svelte';
	import CommandPalette from './CommandPalette.svelte';
	import ChatPanel from './ChatPanel.svelte';
	import { taskState, fetchTasks, createTask } from '$lib/stores/tasks.svelte';
	import { navState, navigate, toggleSidebar, toggleChatPanel, setBoardWidth } from '$lib/stores/nav.svelte';
	import { projects as projectsApi } from '$lib/api/client';
	import type { User, Task, Project } from '$lib/types';

	interface Props {
		user: User;
	}

	let { user }: Props = $props();

	let projects = $state<Project[]>([]);
	let showNewTask = $state(false);
	let selectedTask = $state<Task | null>(null);
	let showCommandPalette = $state(false);
	let showKeyboardHelp = $state(false);
	let keyboardHelpEl: HTMLDialogElement;

	let isResizing = $state(false);
	let containerEl: HTMLDivElement;

	$effect(() => {
		if (showKeyboardHelp && keyboardHelpEl && !keyboardHelpEl.open) {
			keyboardHelpEl.showModal();
		}
	});

	onMount(() => {
		projectsApi.list().then((data) => {
			projects = data;
		}).catch((e) => {
			console.error(e);
		});
	});

	function handleKeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement;
		if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) return;

		// Cmd+K works everywhere
		if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
			e.preventDefault();
			showCommandPalette = !showCommandPalette;
			return;
		}

		// Escape closes dialogs in order
		if (e.key === 'Escape') {
			if (showCommandPalette) showCommandPalette = false;
			else if (showNewTask) showNewTask = false;
			else if (selectedTask) selectedTask = null;
			return;
		}

		// Don't fire single-key shortcuts when dialogs are open
		if (showNewTask || selectedTask || showCommandPalette || showKeyboardHelp) return;

		if (e.key === '?') {
			e.preventDefault();
			showKeyboardHelp = true;
			return;
		}

		if (e.key === '[' && !e.metaKey && !e.ctrlKey) {
			e.preventDefault();
			toggleSidebar();
			return;
		}

		if (e.key === ']' && !e.metaKey && !e.ctrlKey) {
			e.preventDefault();
			toggleChatPanel();
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

	// Resize handlers
	function startResize(e: MouseEvent) {
		e.preventDefault();
		isResizing = true;

		function onMouseMove(e: MouseEvent) {
			if (!containerEl) return;
			const rect = containerEl.getBoundingClientRect();
			const newWidth = ((e.clientX - rect.left) / rect.width) * 100;
			setBoardWidth(Math.max(30, Math.min(80, newWidth)));
		}

		function onMouseUp() {
			isResizing = false;
			document.removeEventListener('mousemove', onMouseMove);
			document.removeEventListener('mouseup', onMouseUp);
		}

		document.addEventListener('mousemove', onMouseMove);
		document.addEventListener('mouseup', onMouseUp);
	}
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="h-full flex flex-col" bind:this={containerEl}>
	<!-- Layout: Board | Resize | Chat -->
	<div class="flex-1 flex min-h-0" class:select-none={isResizing}>
		<!-- Board panel -->
		<div class="overflow-hidden flex flex-col {navState.chatPanelOpen ? 'flex-shrink-0' : 'flex-1'}" style={navState.chatPanelOpen ? `width: ${navState.boardWidth}%` : undefined}>
			<div class="flex-1 overflow-y-auto p-4">
				<TaskBoard
					onTaskClick={handleTaskClick}
					onNewTask={() => (showNewTask = true)}
				/>
			</div>
		</div>

		{#if navState.chatPanelOpen}
			<!-- Resize handle -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="w-1 shrink-0 cursor-col-resize group relative hover:bg-primary/30 transition-colors {isResizing ? 'bg-primary/50' : 'bg-border'}"
				onmousedown={startResize}
			>
				<div class="absolute inset-y-0 -left-1 -right-1"></div>
				<div class="absolute top-1/2 -translate-y-1/2 left-1/2 -translate-x-1/2 w-1 h-8 rounded-full bg-muted-foreground/20 group-hover:bg-primary/50 transition-colors"></div>
			</div>

			<!-- Chat panel -->
			<div class="flex-1 min-w-0 border-l border-border">
				<ChatPanel />
			</div>
		{/if}
	</div>

	<!-- Keyboard shortcuts hint -->
	<div class="hidden lg:flex items-center gap-4 text-xs text-muted-foreground px-4 py-1.5 border-t border-border bg-card/50 shrink-0">
		<span class="flex items-center gap-1">
			<kbd class="kbd">←→↑↓</kbd>
			navigate
		</span>
		<span class="flex items-center gap-1">
			<kbd class="kbd">↵</kbd>
			open
		</span>
		<span class="flex items-center gap-1">
			<kbd class="kbd">N</kbd>
			new
		</span>
		<span class="flex items-center gap-1">
			<kbd class="kbd">[</kbd>
			sidebar
		</span>
		<span class="flex items-center gap-1">
			<kbd class="kbd">]</kbd>
			chat
		</span>
		<span class="flex items-center gap-1">
			<kbd class="kbd">⌘K</kbd>
			search
		</span>
		<button class="flex items-center gap-1 ml-auto hover:text-foreground transition-colors" onclick={() => (showKeyboardHelp = true)}>
			<kbd class="kbd">?</kbd>
			help
		</button>
	</div>
</div>

<!-- Keyboard shortcuts help dialog -->
{#if showKeyboardHelp}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<dialog
		bind:this={keyboardHelpEl}
		class="dialog w-full sm:max-w-md"
		aria-labelledby="keyboard-help-title"
		onclose={() => (showKeyboardHelp = false)}
		onclick={(e) => { if (e.target === keyboardHelpEl) keyboardHelpEl.close(); }}
	>
		<div>
			<header>
				<h2 id="keyboard-help-title">Keyboard Shortcuts</h2>
			</header>
			<section>
				<div class="space-y-4 text-sm">
					<div>
						<h3 class="font-semibold text-xs uppercase tracking-wider text-muted-foreground mb-2">Navigation</h3>
						<div class="space-y-1.5">
							<div class="flex justify-between"><span>Navigate board</span><span class="flex gap-1"><kbd class="kbd">←</kbd><kbd class="kbd">→</kbd><kbd class="kbd">↑</kbd><kbd class="kbd">↓</kbd></span></div>
							<div class="flex justify-between"><span>Open task</span><kbd class="kbd">↵</kbd></div>
							<div class="flex justify-between"><span>Search</span><span class="flex gap-1"><kbd class="kbd">⌘</kbd><kbd class="kbd">K</kbd></span></div>
						</div>
					</div>
					<div>
						<h3 class="font-semibold text-xs uppercase tracking-wider text-muted-foreground mb-2">Actions</h3>
						<div class="space-y-1.5">
							<div class="flex justify-between"><span>New task</span><kbd class="kbd">N</kbd></div>
							<div class="flex justify-between"><span>Refresh</span><kbd class="kbd">R</kbd></div>
						</div>
					</div>
					<div>
						<h3 class="font-semibold text-xs uppercase tracking-wider text-muted-foreground mb-2">Panels</h3>
						<div class="space-y-1.5">
							<div class="flex justify-between"><span>Toggle sidebar</span><kbd class="kbd">[</kbd></div>
							<div class="flex justify-between"><span>Toggle chat</span><kbd class="kbd">]</kbd></div>
						</div>
					</div>
					<div>
						<h3 class="font-semibold text-xs uppercase tracking-wider text-muted-foreground mb-2">General</h3>
						<div class="space-y-1.5">
							<div class="flex justify-between"><span>Close / dismiss</span><kbd class="kbd">Esc</kbd></div>
							<div class="flex justify-between"><span>This help</span><kbd class="kbd">?</kbd></div>
						</div>
					</div>
				</div>
			</section>
			<footer>
				<button class="btn-sm-outline" onclick={() => keyboardHelpEl.close()} title="Close (Esc)">Close</button>
			</footer>
			<button type="button" aria-label="Close dialog" title="Close (Esc)" onclick={() => keyboardHelpEl.close()}>
				<X class="h-4 w-4" />
			</button>
		</div>
	</dialog>
{/if}

<CommandPalette
	isOpen={showCommandPalette}
	onClose={() => (showCommandPalette = false)}
	tasks={taskState.tasks}
	onSelectTask={(task) => (selectedTask = task)}
	onNewTask={() => (showNewTask = true)}
	onNavigate={navigate}
	onToggleSidebar={toggleSidebar}
	onToggleChat={toggleChatPanel}
	onRefreshTasks={fetchTasks}
	onShowKeyboardHelp={() => (showKeyboardHelp = true)}
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
