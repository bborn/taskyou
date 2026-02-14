<script lang="ts">
	import { Search, Clock, Zap, AlertCircle, CheckCircle, Plus, Settings, ArrowRight } from 'lucide-svelte';
	import type { Task } from '$lib/types';

	interface Props {
		isOpen: boolean;
		onClose: () => void;
		tasks: Task[];
		onSelectTask: (task: Task) => void;
		onNewTask: () => void;
		onSettings: () => void;
	}

	let { isOpen, onClose, tasks, onSelectTask, onNewTask, onSettings }: Props = $props();

	let query = $state('');
	let selectedIndex = $state(0);
	let inputEl: HTMLInputElement;

	const statusIcons: Record<string, { icon: typeof Clock; color: string }> = {
		backlog: { icon: Clock, color: 'text-[hsl(var(--status-backlog))]' },
		queued: { icon: Clock, color: 'text-[hsl(var(--status-queued))]' },
		processing: { icon: Zap, color: 'text-[hsl(var(--status-processing))]' },
		blocked: { icon: AlertCircle, color: 'text-[hsl(var(--status-blocked))]' },
		done: { icon: CheckCircle, color: 'text-[hsl(var(--status-done))]' },
	};

	$effect(() => {
		if (isOpen) {
			query = '';
			selectedIndex = 0;
			setTimeout(() => inputEl?.focus(), 50);
		}
	});

	type Item = { id: string; type: 'task' | 'action'; task?: Task; label: string; description?: string; action?: () => void };

	let items = $derived.by((): Item[] => {
		const actions: Item[] = [
			{ id: 'new-task', type: 'action', label: 'New Task', description: 'Create a new task', action: () => { onClose(); onNewTask(); } },
			{ id: 'settings', type: 'action', label: 'Settings', description: 'Open settings', action: () => { onClose(); onSettings(); } },
		];

		const taskItems: Item[] = tasks.map((t) => ({
			id: `task-${t.id}`,
			type: 'task' as const,
			task: t,
			label: t.title,
			description: t.project !== 'personal' ? t.project : undefined,
		}));

		const all = [...actions, ...taskItems];

		if (!query.trim()) return all;
		const q = query.toLowerCase();
		return all.filter(
			(item) =>
				item.label.toLowerCase().includes(q) ||
				(item.description && item.description.toLowerCase().includes(q)),
		);
	});

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			onClose();
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			selectedIndex = Math.min(selectedIndex + 1, items.length - 1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			selectedIndex = Math.max(selectedIndex - 1, 0);
		} else if (e.key === 'Enter' && items[selectedIndex]) {
			e.preventDefault();
			handleSelect(items[selectedIndex]);
		}
	}

	function handleSelect(item: Item) {
		if (item.type === 'action' && item.action) {
			item.action();
		} else if (item.task) {
			onClose();
			onSelectTask(item.task);
		}
	}
</script>

{#if isOpen}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]">
		<div class="absolute inset-0 bg-black/50 backdrop-blur-sm" onclick={onClose}></div>

		<div
			class="card relative w-full max-w-lg overflow-hidden"
			onkeydown={handleKeydown}
		>
			<!-- Search Input -->
			<div class="flex items-center gap-3 px-4 py-3 border-b border-border">
				<Search class="h-5 w-5 text-muted-foreground shrink-0" />
				<input
					bind:this={inputEl}
					bind:value={query}
					placeholder="Search tasks or run a command..."
					class="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
				/>
				<kbd class="kbd">esc</kbd>
			</div>

			<!-- Results -->
			<div class="max-h-[300px] overflow-y-auto scrollbar-thin py-2">
				{#if items.length === 0}
					<div class="px-4 py-8 text-center text-sm text-muted-foreground">
						No results found
					</div>
				{:else}
					{#each items as item, i}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="flex items-center gap-3 px-4 py-2.5 cursor-pointer transition-colors"
							class:bg-accent={i === selectedIndex}
							onclick={() => handleSelect(item)}
							onmouseenter={() => (selectedIndex = i)}
						>
							{#if item.type === 'action'}
								{#if item.id === 'new-task'}
									<Plus class="h-4 w-4 text-primary shrink-0" />
								{:else}
									<Settings class="h-4 w-4 text-muted-foreground shrink-0" />
								{/if}
							{:else if item.task}
								{@const si = statusIcons[item.task.status] || statusIcons.backlog}
								<svelte:component this={si.icon} class="h-4 w-4 shrink-0 {si.color}" />
							{/if}
							<div class="flex-1 min-w-0">
								<div class="text-sm truncate">{item.label}</div>
								{#if item.description}
									<div class="text-xs text-muted-foreground truncate">{item.description}</div>
								{/if}
							</div>
							{#if i === selectedIndex}
								<ArrowRight class="h-4 w-4 text-muted-foreground shrink-0" />
							{/if}
						</div>
					{/each}
				{/if}
			</div>
		</div>
	</div>
{/if}
