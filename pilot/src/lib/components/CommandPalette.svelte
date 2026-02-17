<script lang="ts">
	import {
		Search, Clock, Zap, AlertCircle, CheckCircle, Plus, Settings,
		ArrowRight, LayoutDashboard, FolderOpen, PanelLeft, MessageSquare,
		Link, ShieldCheck, Building2, RefreshCw, Keyboard, XCircle
	} from 'lucide-svelte';
	import type { Task, NavView } from '$lib/types';

	interface Props {
		isOpen: boolean;
		onClose: () => void;
		tasks: Task[];
		onSelectTask: (task: Task) => void;
		onNewTask: () => void;
		onNavigate: (view: NavView) => void;
		onToggleSidebar: () => void;
		onToggleChat: () => void;
		onRefreshTasks: () => void;
		onShowKeyboardHelp: () => void;
	}

	let { isOpen, onClose, tasks, onSelectTask, onNewTask, onNavigate, onToggleSidebar, onToggleChat, onRefreshTasks, onShowKeyboardHelp }: Props = $props();

	let query = $state('');
	let selectedIndex = $state(0);
	let inputEl: HTMLInputElement;

	const statusIcons: Record<string, { icon: typeof Clock; color: string }> = {
		backlog: { icon: Clock, color: 'text-[hsl(var(--status-backlog))]' },
		queued: { icon: Clock, color: 'text-[hsl(var(--status-queued))]' },
		processing: { icon: Zap, color: 'text-[hsl(var(--status-processing))]' },
		blocked: { icon: AlertCircle, color: 'text-[hsl(var(--status-blocked))]' },
		done: { icon: CheckCircle, color: 'text-[hsl(var(--status-done))]' },
		failed: { icon: XCircle, color: 'text-[hsl(var(--status-blocked))]' },
	};

	$effect(() => {
		if (isOpen) {
			query = '';
			selectedIndex = 0;
			setTimeout(() => inputEl?.focus(), 50);
		}
	});

	type Item = {
		id: string;
		type: 'task' | 'action';
		task?: Task;
		label: string;
		description?: string;
		shortcut?: string;
		icon: typeof Clock;
		iconColor?: string;
		action?: () => void;
	};

	const actions: Item[] = [
		{ id: 'new-task', type: 'action', label: 'New Task', description: 'Create a new task', shortcut: 'N', icon: Plus, iconColor: 'text-primary', action: () => { onClose(); onNewTask(); } },
		{ id: 'go-dashboard', type: 'action', label: 'Go to Dashboard', description: 'View task board', shortcut: '1', icon: LayoutDashboard, iconColor: 'text-blue-500', action: () => { onClose(); onNavigate('dashboard'); } },
		{ id: 'go-projects', type: 'action', label: 'Go to Projects', description: 'Manage projects & sandboxes', shortcut: '2', icon: FolderOpen, iconColor: 'text-green-500', action: () => { onClose(); onNavigate('projects'); } },
		{ id: 'go-integrations', type: 'action', label: 'Go to Integrations', description: 'Connected services', icon: Link, iconColor: 'text-purple-500', action: () => { onClose(); onNavigate('integrations'); } },
		{ id: 'go-approvals', type: 'action', label: 'Go to Approvals', description: 'Review agent actions', icon: ShieldCheck, iconColor: 'text-amber-500', action: () => { onClose(); onNavigate('approvals'); } },
		{ id: 'go-workspaces', type: 'action', label: 'Go to Workspaces', description: 'Manage workspaces', icon: Building2, iconColor: 'text-cyan-500', action: () => { onClose(); onNavigate('workspaces'); } },
		{ id: 'go-settings', type: 'action', label: 'Settings', description: 'Preferences & projects', icon: Settings, iconColor: 'text-muted-foreground', action: () => { onClose(); onNavigate('settings'); } },
		{ id: 'toggle-sidebar', type: 'action', label: 'Toggle Sidebar', description: 'Show or hide the sidebar', shortcut: '[', icon: PanelLeft, iconColor: 'text-muted-foreground', action: () => { onClose(); onToggleSidebar(); } },
		{ id: 'toggle-chat', type: 'action', label: 'Toggle Chat Panel', description: 'Show or hide the chat panel', shortcut: ']', icon: MessageSquare, iconColor: 'text-muted-foreground', action: () => { onClose(); onToggleChat(); } },
		{ id: 'refresh-tasks', type: 'action', label: 'Refresh Tasks', description: 'Reload all tasks', shortcut: 'R', icon: RefreshCw, iconColor: 'text-muted-foreground', action: () => { onClose(); onRefreshTasks(); } },
		{ id: 'keyboard-help', type: 'action', label: 'Keyboard Shortcuts', description: 'View all shortcuts', shortcut: '?', icon: Keyboard, iconColor: 'text-muted-foreground', action: () => { onClose(); onShowKeyboardHelp(); } },
	];

	let items = $derived.by((): Item[] => {
		const taskItems: Item[] = tasks.map((t) => ({
			id: `task-${t.id}`,
			type: 'task' as const,
			task: t,
			label: t.title,
			description: t.project_id || t.status,
			icon: (statusIcons[t.status] || statusIcons.backlog).icon,
			iconColor: (statusIcons[t.status] || statusIcons.backlog).color,
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

	// Reset index when items change
	$effect(() => {
		if (items.length > 0 && selectedIndex >= items.length) {
			selectedIndex = 0;
		}
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
			<div class="max-h-[300px] overflow-y-auto scrollbar-thin py-1">
				{#if items.length === 0}
					<div class="px-4 py-8 text-center text-sm text-muted-foreground">
						No results found
					</div>
				{:else}
					{#each items as item, i}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="flex items-center gap-3 px-4 py-2 cursor-pointer transition-colors"
							class:bg-accent={i === selectedIndex}
							onclick={() => handleSelect(item)}
							onmouseenter={() => (selectedIndex = i)}
						>
							<item.icon class="h-4 w-4 shrink-0 {item.iconColor || 'text-muted-foreground'}" />
							<div class="flex-1 min-w-0">
								<div class="text-sm truncate">{item.label}</div>
								{#if item.description}
									<div class="text-xs text-muted-foreground truncate">{item.description}</div>
								{/if}
							</div>
							{#if item.shortcut}
								<kbd class="kbd text-[10px]">{item.shortcut}</kbd>
							{/if}
							{#if i === selectedIndex}
								<ArrowRight class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
							{/if}
						</div>
					{/each}
				{/if}
			</div>
		</div>
	</div>
{/if}
