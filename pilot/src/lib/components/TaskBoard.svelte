<script lang="ts">
	import { Plus, Inbox, Zap, AlertCircle, CheckCircle, XCircle, Play, RotateCcw, Pencil, Trash2, Copy, ExternalLink, X } from 'lucide-svelte';
	import {
		getBacklogTasks, getInProgressTasks, getBlockedTasks, getDoneTasks, getFailedTasks,
		updateTask, deleteTask, fetchTasks,
	} from '$lib/stores/tasks.svelte';
	import { navState, setFocus } from '$lib/stores/nav.svelte';
	import TaskCard from './TaskCard.svelte';
	import ContextMenu from './ContextMenu.svelte';
	import type { Task, TaskStatus } from '$lib/types';

	interface Props {
		onTaskClick: (task: Task) => void;
		onNewTask: () => void;
	}

	let { onTaskClick, onNewTask }: Props = $props();

	// Drag-and-drop state
	let draggedTask = $state<Task | null>(null);
	let dragOverColumn = $state<string | null>(null);

	// Multi-select state
	let selectedIds = $state(new Set<number>());

	let selectedCount = $derived(selectedIds.size);

	function toggleSelect(taskId: number) {
		const next = new Set(selectedIds);
		if (next.has(taskId)) {
			next.delete(taskId);
		} else {
			next.add(taskId);
		}
		selectedIds = next;
	}

	function clearSelection() {
		selectedIds = new Set();
	}

	function isSelected(taskId: number): boolean {
		return selectedIds.has(taskId);
	}

	// Bulk actions
	async function bulkRun() {
		const ids = [...selectedIds];
		clearSelection();
		for (const id of ids) {
			await updateTask(id, { status: 'queued' });
		}
	}

	async function bulkDelete() {
		const count = selectedIds.size;
		if (!confirm(`Delete ${count} task${count > 1 ? 's' : ''}?`)) return;
		const ids = [...selectedIds];
		clearSelection();
		for (const id of ids) {
			await deleteTask(id);
		}
		fetchTasks();
	}

	async function bulkMarkDone() {
		const ids = [...selectedIds];
		clearSelection();
		for (const id of ids) {
			await updateTask(id, { status: 'done' });
		}
	}

	const columns = [
		{ key: 'backlog', title: 'Backlog', icon: Inbox, emptyMessage: 'No tasks waiting', color: 'hsl(var(--status-backlog))', showAdd: true, targetStatus: 'backlog' as TaskStatus },
		{ key: 'running', title: 'Running', icon: Zap, emptyMessage: 'Nothing running', color: 'hsl(var(--status-processing))', showAdd: false, targetStatus: 'queued' as TaskStatus },
		{ key: 'blocked', title: 'Blocked', icon: AlertCircle, emptyMessage: 'All clear!', color: 'hsl(var(--status-blocked))', showAdd: false, targetStatus: 'blocked' as TaskStatus },
		{ key: 'done', title: 'Done', icon: CheckCircle, emptyMessage: 'Nothing completed', color: 'hsl(var(--status-done))', showAdd: false, targetStatus: 'done' as TaskStatus },
		{ key: 'failed', title: 'Failed', icon: XCircle, emptyMessage: 'No failures', color: 'hsl(var(--status-blocked))', showAdd: false, targetStatus: 'failed' as TaskStatus },
	];

	function getColumnTasks(key: string): Task[] {
		switch (key) {
			case 'backlog': return getBacklogTasks();
			case 'running': return getInProgressTasks();
			case 'blocked': return getBlockedTasks();
			case 'done': return getDoneTasks();
			case 'failed': return getFailedTasks();
			default: return [];
		}
	}

	// Drag handlers
	function handleDragStart(e: DragEvent, task: Task) {
		draggedTask = task;
		if (e.dataTransfer) {
			e.dataTransfer.effectAllowed = 'move';
			e.dataTransfer.setData('text/plain', String(task.id));
		}
	}

	function handleDragOver(e: DragEvent, columnKey: string) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		dragOverColumn = columnKey;
	}

	function handleDragLeave() {
		dragOverColumn = null;
	}

	async function handleDrop(e: DragEvent, targetStatus: TaskStatus) {
		e.preventDefault();
		dragOverColumn = null;

		if (!draggedTask) return;

		const currentStatus = draggedTask.status;
		if (currentStatus === targetStatus) {
			draggedTask = null;
			return;
		}

		await updateTask(draggedTask.id, { status: targetStatus });
		draggedTask = null;
	}

	function handleDragEnd() {
		draggedTask = null;
		dragOverColumn = null;
	}

	// Context menu state
	let contextMenu = $state<{ x: number; y: number; task: Task } | null>(null);

	function handleContextMenu(e: MouseEvent, task: Task) {
		e.preventDefault();
		contextMenu = { x: e.clientX, y: e.clientY, task };
	}

	function getContextMenuItems(task: Task) {
		const items: { label: string; icon?: typeof Play; action: () => void; variant?: 'default' | 'destructive'; separator?: boolean }[] = [];

		items.push({ label: 'View Details', icon: ExternalLink, action: () => onTaskClick(task) });
		items.push({ label: 'Edit', icon: Pencil, action: () => onTaskClick(task) });

		if (task.status === 'backlog') {
			items.push({ label: 'Queue', icon: Play, action: () => updateTask(task.id, { status: 'queued' }) });
		}
		if (task.status === 'blocked' || task.status === 'failed') {
			items.push({ label: 'Retry', icon: RotateCcw, action: () => updateTask(task.id, { status: 'queued' }) });
		}
		if (task.status === 'processing' || task.status === 'blocked') {
			items.push({ label: 'Mark Done', icon: CheckCircle, action: () => updateTask(task.id, { status: 'done' }) });
		}

		items.push({ label: 'Copy ID', icon: Copy, action: () => navigator.clipboard.writeText(String(task.id)), separator: true });

		items.push({ label: 'Delete', icon: Trash2, action: () => deleteTask(task.id), variant: 'destructive', separator: true });

		return items;
	}

	async function moveTaskToColumn(task: Task, targetColIdx: number) {
		const targetStatus = columns[targetColIdx].targetStatus;
		if (task.status === targetStatus) return;

		await updateTask(task.id, { status: targetStatus });
		setFocus(targetColIdx, navState.focusedRow);
	}

	async function deleteFocusedTask(task: Task) {
		if (!confirm(`Delete "${task.title}"?`)) return;
		await deleteTask(task.id);
		const remaining = getColumnTasks(columns[navState.focusedColumn].key);
		setFocus(navState.focusedColumn, Math.min(navState.focusedRow, Math.max(0, remaining.length - 2)));
	}

	// Keyboard navigation
	function handleKeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement;
		if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) return;
		if (document.querySelector('dialog[open]')) return;

		const col = navState.focusedColumn;
		const row = navState.focusedRow;
		const allColumnTasks = columns.map(c => getColumnTasks(c.key));
		const currentTasks = allColumnTasks[col];
		const focusedTask = currentTasks?.[row];

		// Cmd+Enter — queue focused task
		if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
			if (focusedTask && (focusedTask.status === 'backlog' || focusedTask.status === 'blocked' || focusedTask.status === 'failed')) {
				e.preventDefault();
				updateTask(focusedTask.id, { status: 'queued' });
			}
			return;
		}

		// Cmd+Delete / Cmd+Backspace — delete focused task
		if ((e.metaKey || e.ctrlKey) && (e.key === 'Delete' || e.key === 'Backspace')) {
			e.preventDefault();
			if (focusedTask) {
				deleteFocusedTask(focusedTask);
			}
			return;
		}

		// Shift+Arrow — move task between columns
		if (e.shiftKey && (e.key === 'ArrowLeft' || e.key === 'H')) {
			if (focusedTask && col > 0) {
				e.preventDefault();
				moveTaskToColumn(focusedTask, col - 1);
			}
			return;
		}
		if (e.shiftKey && (e.key === 'ArrowRight' || e.key === 'L')) {
			if (focusedTask && col < columns.length - 1) {
				e.preventDefault();
				moveTaskToColumn(focusedTask, col + 1);
			}
			return;
		}

		switch (e.key) {
			case 'x': {
				// Toggle select on focused task
				if (focusedTask) {
					e.preventDefault();
					toggleSelect(focusedTask.id);
				}
				break;
			}
			case 'Escape': {
				if (selectedCount > 0) {
					e.preventDefault();
					clearSelection();
				}
				break;
			}
			case 'h':
			case 'ArrowLeft': {
				e.preventDefault();
				const newCol = Math.max(0, col - 1);
				setFocus(newCol, Math.min(row, Math.max(0, allColumnTasks[newCol].length - 1)));
				break;
			}
			case 'l':
			case 'ArrowRight': {
				e.preventDefault();
				const newCol = Math.min(columns.length - 1, col + 1);
				setFocus(newCol, Math.min(row, Math.max(0, allColumnTasks[newCol].length - 1)));
				break;
			}
			case 'j':
			case 'ArrowDown':
				e.preventDefault();
				setFocus(col, Math.min(row + 1, Math.max(0, allColumnTasks[col].length - 1)));
				break;
			case 'k':
			case 'ArrowUp':
				e.preventDefault();
				setFocus(col, Math.max(0, row - 1));
				break;
			case 'Enter': {
				e.preventDefault();
				if (focusedTask) onTaskClick(focusedTask);
				break;
			}
		}
	}
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="h-full flex flex-col">
	<!-- Bulk action bar -->
	{#if selectedCount > 0}
		<div class="flex items-center gap-3 mb-3 px-3 py-2 rounded-lg bg-primary/10 border border-primary/20 shrink-0">
			<span class="text-sm font-medium">{selectedCount} selected</span>
			<div class="flex items-center gap-1.5 ml-auto">
				<button class="btn-sm" onclick={bulkRun} title="Run selected tasks">
					<Play class="h-3.5 w-3.5" />
					Run
				</button>
				<button class="btn-sm-secondary" onclick={bulkMarkDone} title="Mark selected as done">
					<CheckCircle class="h-3.5 w-3.5" />
					Done
				</button>
				<button class="btn-sm-destructive" onclick={bulkDelete} title="Delete selected tasks">
					<Trash2 class="h-3.5 w-3.5" />
					Delete
				</button>
				<button class="btn-sm-ghost" onclick={clearSelection} title="Clear selection (Esc)">
					<X class="h-3.5 w-3.5" />
				</button>
			</div>
		</div>
	{:else}
		<!-- Header -->
		<div class="flex items-center justify-between mb-4 shrink-0">
			<div class="flex items-center gap-3">
				<h1 class="text-xl font-bold text-gradient">Tasks</h1>
				<div class="flex items-center gap-2 text-sm text-muted-foreground">
					{#if getInProgressTasks().length > 0}
						<span class="badge-outline text-xs" style="color: var(--status-processing); border-color: var(--status-processing);">
							<Zap class="h-3 w-3" />
							{getInProgressTasks().length}
						</span>
					{/if}
					{#if getBlockedTasks().length > 0}
						<span class="badge-outline text-xs" style="color: var(--status-blocked); border-color: var(--status-blocked);">
							<AlertCircle class="h-3 w-3" />
							{getBlockedTasks().length}
						</span>
					{/if}
				</div>
			</div>
			<button class="btn-sm" onclick={onNewTask} title="New task (N)">
				<Plus class="h-3.5 w-3.5" />
				New
			</button>
		</div>
	{/if}

	<!-- Kanban Board -->
	<div class="grid grid-cols-1 md:grid-cols-3 lg:grid-cols-5 gap-3 flex-1 min-h-0">
		{#each columns as col, colIdx}
			{@const columnTasks = getColumnTasks(col.key)}
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="flex flex-col rounded-xl border bg-card/50 overflow-hidden transition-colors {dragOverColumn === col.key ? 'border-primary bg-primary/5' : ''} {navState.focusedColumn === colIdx && columnTasks.length === 0 ? 'ring-2 ring-primary/50' : ''}"
				ondragover={(e) => handleDragOver(e, col.key)}
				ondragleave={handleDragLeave}
				ondrop={(e) => handleDrop(e, col.targetStatus)}
			>
				<!-- Column Header -->
				<div
					class="flex items-center justify-between px-3 py-2 border-b"
					style:border-bottom-color={columnTasks.length > 0 ? col.color : undefined}
					style:border-bottom-width={columnTasks.length > 0 ? '2px' : '1px'}
				>
					<div class="flex items-center gap-1.5">
						<span style:color={col.color}><col.icon class="h-3.5 w-3.5" /></span>
						<h2 class="font-semibold text-xs">{col.title}</h2>
					</div>
					<span class="badge-secondary text-[10px]">
						{columnTasks.length}
					</span>
				</div>

				<!-- Column Content -->
				<div class="flex-1 overflow-y-auto p-2 space-y-1.5 scrollbar-thin">
					{#each columnTasks as task, rowIdx (task.id)}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							draggable="true"
							ondragstart={(e) => handleDragStart(e, task)}
							ondragend={handleDragEnd}
							oncontextmenu={(e) => handleContextMenu(e, task)}
							class="rounded-lg"
							class:ring-2={navState.focusedColumn === colIdx && navState.focusedRow === rowIdx && !isSelected(task.id)}
							class:ring-primary={navState.focusedColumn === colIdx && navState.focusedRow === rowIdx && !isSelected(task.id)}
							class:ring-2-selected={isSelected(task.id)}
							class:opacity-50={draggedTask?.id === task.id}
						>
							<TaskCard
								{task}
								selected={isSelected(task.id)}
								onClick={onTaskClick}
							/>
						</div>
					{/each}

					{#if columnTasks.length === 0}
						<div class="flex flex-col items-center justify-center py-8 text-muted-foreground">
							<col.icon class="h-6 w-6 mb-1.5 opacity-20" />
							<p class="text-xs">{col.emptyMessage}</p>
						</div>
					{/if}
				</div>

				<!-- Quick add for backlog -->
				{#if col.showAdd}
					<div class="p-2 border-t">
						<button class="btn-sm-ghost w-full justify-start" onclick={onNewTask} title="Add task (N)">
							<Plus class="h-3.5 w-3.5" />
							Add task
						</button>
					</div>
				{/if}
			</div>
		{/each}
	</div>
</div>

{#if contextMenu}
	<ContextMenu
		x={contextMenu.x}
		y={contextMenu.y}
		items={getContextMenuItems(contextMenu.task)}
		onClose={() => (contextMenu = null)}
	/>
{/if}
