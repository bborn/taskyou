<script lang="ts">
	import { Loader2 } from 'lucide-svelte';
	import type { Task } from '$lib/types';

	interface Props {
		task: Task;
		selected?: boolean;
		onClick: (task: Task) => void;
	}

	let { task, selected = false, onClick }: Props = $props();

	let isRunning = $derived(task.status === 'processing' || task.status === 'queued');

	function timeAgo(dateStr: string): string {
		const seconds = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
		if (seconds < 60) return 'just now';
		const minutes = Math.floor(seconds / 60);
		if (minutes < 60) return `${minutes}m ago`;
		const hours = Math.floor(minutes / 60);
		if (hours < 24) return `${hours}h ago`;
		const days = Math.floor(hours / 24);
		return `${days}d ago`;
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="rounded-lg border p-2.5 hover:bg-accent/50 transition-colors cursor-pointer {selected ? 'border-primary bg-primary/5' : 'border-border bg-card'}"
	onclick={() => onClick(task)}
>
	<!-- Title row -->
	<div class="flex items-start justify-between gap-2">
		<h4 class="text-sm font-medium leading-tight flex-1">{task.title}</h4>
		{#if isRunning}
			<Loader2 class="size-3 animate-spin text-primary flex-shrink-0 mt-0.5" />
		{/if}
	</div>

	<!-- Metadata badges -->
	<div class="flex flex-wrap items-center gap-1 mt-1.5">
		{#if task.type}
			<span class="badge-outline text-[10px]">{task.type}</span>
		{/if}
	</div>

	<!-- Footer: ID + timestamp -->
	<div class="text-[10px] text-muted-foreground mt-1.5">
		#{task.id}
		{#if task.started_at && isRunning}
			&middot; Started {timeAgo(task.started_at)}
		{:else if task.completed_at}
			&middot; {timeAgo(task.completed_at)}
		{:else if task.created_at}
			&middot; {timeAgo(task.created_at)}
		{/if}
	</div>
</div>
