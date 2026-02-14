<script lang="ts">
	import { Plus, Inbox, Zap, AlertCircle, CheckCircle } from 'lucide-svelte';
	import { getBacklogTasks, getInProgressTasks, getBlockedTasks, getDoneTasks } from '$lib/stores/tasks.svelte';
	import TaskCard from './TaskCard.svelte';
	import type { Task } from '$lib/types';

	interface Props {
		onQueue: (id: number) => void;
		onRetry: (id: number) => void;
		onClose: (id: number) => void;
		onTaskClick: (task: Task) => void;
		onNewTask: () => void;
	}

	let { onQueue, onRetry, onClose, onTaskClick, onNewTask }: Props = $props();
</script>

<div class="h-full">
	<!-- Header -->
	<div class="flex items-center justify-between mb-6">
		<div class="flex items-center gap-4">
			<h1 class="text-2xl font-bold text-gradient">Tasks</h1>
			<div class="flex items-center gap-2 text-sm text-muted-foreground">
				{#if getInProgressTasks().length > 0}
					<span class="flex items-center gap-1 px-2 py-1 rounded-full bg-[hsl(var(--status-processing-bg))] text-[hsl(var(--status-processing))]">
						<Zap class="h-3.5 w-3.5" />
						{getInProgressTasks().length} running
					</span>
				{/if}
				{#if getBlockedTasks().length > 0}
					<span class="flex items-center gap-1 px-2 py-1 rounded-full bg-[hsl(var(--status-blocked-bg))] text-[hsl(var(--status-blocked))]">
						<AlertCircle class="h-3.5 w-3.5" />
						{getBlockedTasks().length} blocked
					</span>
				{/if}
			</div>
		</div>
		<button
			onclick={onNewTask}
			class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground font-medium shadow-lg hover:shadow-xl transition-shadow"
		>
			<Plus class="h-4 w-4" />
			New Task
		</button>
	</div>

	<!-- Kanban Board -->
	<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4 h-[calc(100vh-180px)]">
		<!-- Backlog Column -->
		{@render column('Backlog', Inbox, 'No tasks waiting', 'hsl(var(--status-backlog))', getBacklogTasks(), true)}

		<!-- In Progress Column -->
		{@render column('In Progress', Zap, 'Nothing running', 'hsl(var(--status-processing))', getInProgressTasks(), false)}

		<!-- Needs Attention Column -->
		{@render column('Needs Attention', AlertCircle, 'All clear!', 'hsl(var(--status-blocked))', getBlockedTasks(), false)}

		<!-- Completed Column -->
		{@render column('Completed', CheckCircle, 'Nothing completed yet', 'hsl(var(--status-done))', getDoneTasks(), false)}
	</div>
</div>

{#snippet column(title: string, Icon: typeof Inbox, emptyMessage: string, accentColor: string, columnTasks: Task[], showAdd: boolean)}
	<div class="flex flex-col rounded-xl border bg-card/50 overflow-hidden">
		<!-- Column Header -->
		<div
			class="flex items-center justify-between px-4 py-3 border-b"
			style:border-bottom-color={columnTasks.length > 0 ? accentColor : undefined}
			style:border-bottom-width={columnTasks.length > 0 ? '2px' : '1px'}
		>
			<div class="flex items-center gap-2">
				<span style:color={accentColor}><Icon class="h-4 w-4" /></span>
				<h2 class="font-semibold text-sm">{title}</h2>
			</div>
			<span class="text-xs font-medium px-2 py-0.5 rounded-full {columnTasks.length > 0 ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground'}">
				{columnTasks.length}
			</span>
		</div>

		<!-- Column Content -->
		<div class="flex-1 overflow-y-auto p-3 space-y-2 scrollbar-thin">
			{#each columnTasks as task (task.id)}
				<TaskCard
					{task}
					{onQueue}
					{onRetry}
					{onClose}
					onClick={onTaskClick}
				/>
			{/each}

			{#if columnTasks.length === 0}
				<div class="flex flex-col items-center justify-center py-12 text-muted-foreground">
					<Icon class="h-8 w-8 mb-2 opacity-30" />
					<p class="text-sm">{emptyMessage}</p>
				</div>
			{/if}
		</div>

		<!-- Quick add button for backlog -->
		{#if showAdd}
			<div class="p-3 border-t">
				<button
					class="w-full flex items-center justify-start gap-2 px-3 py-2 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
					onclick={onNewTask}
				>
					<Plus class="h-4 w-4" />
					Add task
				</button>
			</div>
		{/if}
	</div>
{/snippet}
