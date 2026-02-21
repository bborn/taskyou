<script lang="ts">
	import { Loader2 } from 'lucide-svelte';
	import type { Task } from '$lib/types';

	interface Props {
		task: Task;
		onClick: (task: Task) => void;
	}

	let { task, onClick }: Props = $props();

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
	class="rounded-lg px-2.5 py-2 cursor-pointer transition-colors hover:bg-accent/60"
	onclick={() => onClick(task)}
>
	<div class="flex items-start justify-between gap-2">
		<h4 class="text-[13px] font-medium leading-snug flex-1">{task.title}</h4>
		{#if isRunning}
			<Loader2 class="size-3 animate-spin text-primary flex-shrink-0 mt-0.5" />
		{/if}
	</div>

	<div class="flex items-center gap-1.5 mt-1 whitespace-nowrap overflow-hidden">
		{#if task.type}
			<span class="text-[10px] font-medium text-muted-foreground/60 bg-muted px-1.5 py-0.5 rounded">{task.type}</span>
		{/if}
		<span class="text-[10px] text-muted-foreground/40 tabular-nums truncate">#{task.id}{#if task.started_at && isRunning} · {timeAgo(task.started_at)}{:else if task.completed_at} · {timeAgo(task.completed_at)}{:else if task.created_at} · {timeAgo(task.created_at)}{/if}</span>
	</div>
</div>
