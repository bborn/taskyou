<script lang="ts">
	import { Clock, Zap, AlertCircle, CheckCircle, Play, RotateCcw, ShieldAlert, GitPullRequest } from 'lucide-svelte';
	import type { Task, TaskStatus } from '$lib/types';

	interface Props {
		task: Task;
		onQueue: (id: number) => void;
		onRetry: (id: number) => void;
		onClose: (id: number) => void;
		onClick: (task: Task) => void;
	}

	let { task, onQueue, onRetry, onClose, onClick }: Props = $props();

	const statusConfig: Record<TaskStatus, { label: string; colorClass: string; borderClass: string; animate: boolean }> = {
		backlog: { label: 'Backlog', colorClass: 'text-[hsl(var(--status-backlog))]', borderClass: 'border-status-backlog', animate: false },
		queued: { label: 'Queued', colorClass: 'text-[hsl(var(--status-queued))]', borderClass: 'border-status-queued', animate: true },
		processing: { label: 'Running', colorClass: 'text-[hsl(var(--status-processing))]', borderClass: 'border-status-processing', animate: true },
		blocked: { label: 'Blocked', colorClass: 'text-[hsl(var(--status-blocked))]', borderClass: 'border-status-blocked', animate: false },
		done: { label: 'Done', colorClass: 'text-[hsl(var(--status-done))]', borderClass: 'border-status-done', animate: false },
	};

	let config = $derived(statusConfig[task.status]);
	let isActive = $derived(task.status === 'processing' || task.status === 'queued');

	function handleAction(e: MouseEvent, action: () => void) {
		e.stopPropagation();
		action();
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="group cursor-pointer rounded-lg border bg-card p-4 transition-all duration-200 hover:bg-accent/50 hover:shadow-md {config.borderClass}"
	class:glow-primary={isActive}
	onclick={() => onClick(task)}
>
	<!-- Header with status -->
	<div class="flex items-start justify-between gap-2 mb-2">
		<div class="flex items-center gap-2 min-w-0">
			<span class="shrink-0 {config.colorClass}" class:animate-pulse={config.animate}>
				{#if task.status === 'processing'}
					<Zap class="h-4 w-4" />
				{:else if task.status === 'blocked'}
					<AlertCircle class="h-4 w-4" />
				{:else if task.status === 'done'}
					<CheckCircle class="h-4 w-4" />
				{:else}
					<Clock class="h-4 w-4" />
				{/if}
			</span>
			<span class="text-xs font-medium {config.colorClass}">{config.label}</span>
		</div>
		<div class="flex items-center gap-1.5 shrink-0">
			{#if task.dangerous_mode}
				<span title="Dangerous mode">
					<ShieldAlert class="h-3.5 w-3.5 text-orange-500" />
				</span>
			{/if}
			{#if task.pr_url}
				<a
					href={task.pr_url}
					target="_blank"
					rel="noopener noreferrer"
					onclick={(e) => e.stopPropagation()}
					class="flex items-center gap-1 text-xs text-blue-500 hover:text-blue-600 hover:underline"
				>
					<GitPullRequest class="h-3 w-3" />
					<span>#{task.pr_number}</span>
				</a>
			{/if}
		</div>
	</div>

	<!-- Title -->
	<h3 class="font-medium text-sm leading-snug line-clamp-2 mb-2 group-hover:text-primary transition-colors">
		{task.title}
	</h3>

	<!-- Description preview -->
	{#if task.body}
		<p class="text-xs text-muted-foreground line-clamp-2 mb-3">{task.body}</p>
	{/if}

	<!-- Tags -->
	<div class="flex flex-wrap items-center gap-1.5 mb-3">
		{#if task.project && task.project !== 'personal'}
			<span class="text-[10px] px-1.5 py-0 h-5 inline-flex items-center rounded-full border border-input">{task.project}</span>
		{/if}
		{#if task.type}
			<span class="text-[10px] px-1.5 py-0 h-5 inline-flex items-center rounded-full bg-secondary text-secondary-foreground">{task.type}</span>
		{/if}
	</div>

	<!-- Action buttons -->
	<div class="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
		{#if task.status === 'backlog'}
			<button
				class="h-7 px-2 text-xs gap-1 inline-flex items-center rounded-md hover:bg-primary hover:text-primary-foreground transition-colors"
				onclick={(e) => handleAction(e, () => onQueue(task.id))}
			>
				<Play class="h-3 w-3" />
				Run
			</button>
		{/if}
		{#if task.status === 'blocked'}
			<button
				class="h-7 px-2 text-xs gap-1 inline-flex items-center rounded-md hover:bg-orange-500 hover:text-white transition-colors"
				onclick={(e) => handleAction(e, () => onRetry(task.id))}
			>
				<RotateCcw class="h-3 w-3" />
				Retry
			</button>
		{/if}
		{#if task.status === 'processing' || task.status === 'blocked'}
			<button
				class="h-7 px-2 text-xs gap-1 inline-flex items-center rounded-md hover:bg-green-500 hover:text-white transition-colors"
				onclick={(e) => handleAction(e, () => onClose(task.id))}
			>
				<CheckCircle class="h-3 w-3" />
				Done
			</button>
		{/if}
	</div>
</div>
