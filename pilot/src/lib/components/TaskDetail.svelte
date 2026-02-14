<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import {
		X, Play, RotateCcw, CheckCircle, Trash2, Edit3, Save,
		ShieldAlert, GitPullRequest, GitBranch, Clock, Calendar,
		Zap, AlertCircle, Terminal, ChevronDown, ChevronUp, MessageSquare, ExternalLink,
	} from 'lucide-svelte';
	import { tasks as tasksApi } from '$lib/api/client';
	import type { Task, TaskLog, TaskStatus } from '$lib/types';

	interface Props {
		task: Task;
		onClose: () => void;
		onUpdate: (task: Task) => void;
		onDelete?: () => void;
	}

	let { task: initialTask, onClose, onUpdate, onDelete }: Props = $props();

	let task = $state(initialTask);
	let logs = $state<TaskLog[]>([]);
	let loading = $state(false);
	let actionLoading = $state<string | null>(null);
	let isEditing = $state(false);
	let editTitle = $state(task.title);
	let editBody = $state(task.body || '');
	let showRetryDialog = $state(false);
	let retryFeedback = $state('');
	let logsExpanded = $state(true);
	let logsEndEl: HTMLDivElement;
	let pollInterval: ReturnType<typeof setInterval> | null = null;

	$effect(() => {
		task = initialTask;
		editTitle = initialTask.title;
		editBody = initialTask.body || '';
	});

	const statusConfig: Record<TaskStatus, { label: string; colorClass: string; bgClass: string }> = {
		backlog: { label: 'Backlog', colorClass: 'text-[hsl(var(--status-backlog))]', bgClass: 'bg-[hsl(var(--status-backlog))]' },
		queued: { label: 'Queued', colorClass: 'text-[hsl(var(--status-queued))]', bgClass: 'bg-[hsl(var(--status-queued))]' },
		processing: { label: 'Running', colorClass: 'text-[hsl(var(--status-processing))]', bgClass: 'bg-[hsl(var(--status-processing))]' },
		blocked: { label: 'Blocked', colorClass: 'text-[hsl(var(--status-blocked))]', bgClass: 'bg-[hsl(var(--status-blocked))]' },
		done: { label: 'Done', colorClass: 'text-[hsl(var(--status-done))]', bgClass: 'bg-[hsl(var(--status-done))]' },
	};

	const logTypeColors: Record<string, string> = {
		error: 'text-red-400',
		system: 'text-yellow-400',
		tool: 'text-cyan-400',
		output: 'text-green-400',
		text: 'text-gray-300',
	};

	let config = $derived(statusConfig[task.status]);

	async function fetchLogs() {
		try {
			const taskLogs = await tasksApi.getLogs(task.id, 200);
			logs = taskLogs.reverse();
		} catch (err) {
			console.error('Failed to fetch logs:', err);
		}
	}

	onMount(async () => {
		loading = true;
		await fetchLogs();
		loading = false;

		if (task.status === 'processing' || task.status === 'queued') {
			pollInterval = setInterval(fetchLogs, 1500);
		}
	});

	onDestroy(() => {
		if (pollInterval) clearInterval(pollInterval);
	});

	$effect(() => {
		if (logsExpanded && logsEndEl) {
			logsEndEl.scrollIntoView({ behavior: 'smooth' });
		}
	});

	async function handleQueue() {
		actionLoading = 'queue';
		try {
			const updated = await tasksApi.queue(task.id);
			task = updated;
			onUpdate(updated);
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleRetry() {
		actionLoading = 'retry';
		try {
			const updated = await tasksApi.retry(task.id, retryFeedback || undefined);
			task = updated;
			onUpdate(updated);
			showRetryDialog = false;
			retryFeedback = '';
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleClose() {
		actionLoading = 'close';
		try {
			const updated = await tasksApi.close(task.id);
			task = updated;
			onUpdate(updated);
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleDelete() {
		if (!confirm('Are you sure you want to delete this task?')) return;
		actionLoading = 'delete';
		try {
			await tasksApi.delete(task.id);
			onDelete?.();
			onClose();
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleSaveEdit() {
		if (!editTitle.trim()) return;
		actionLoading = 'save';
		try {
			const updated = await tasksApi.update(task.id, { title: editTitle.trim(), body: editBody.trim() });
			task = updated;
			onUpdate(updated);
			isEditing = false;
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	function formatDate(date: string) {
		return new Date(date).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
	}

	function formatDuration(start: string, end?: string) {
		const diffMs = (end ? new Date(end) : new Date()).getTime() - new Date(start).getTime();
		const mins = Math.floor(diffMs / 60000);
		const hours = Math.floor(mins / 60);
		return hours > 0 ? `${hours}h ${mins % 60}m` : `${mins}m`;
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="fixed inset-0 z-50 flex">
	<!-- Backdrop -->
	<div class="absolute inset-0 bg-black/60 backdrop-blur-sm" onclick={onClose}></div>

	<!-- Panel -->
	<div class="relative ml-auto w-full max-w-2xl bg-background border-l border-border flex flex-col h-full shadow-2xl">
		<!-- Header -->
		<div class="flex items-start gap-4 p-5 border-b border-border">
			<div class="flex-1 min-w-0">
				{#if isEditing}
					<input
						bind:value={editTitle}
						class="w-full text-xl font-semibold mb-2 px-3 py-1 rounded-lg border border-input bg-background"
					/>
				{:else}
					<h2 class="text-xl font-semibold mb-2 pr-8">{task.title}</h2>
				{/if}

				<div class="flex flex-wrap items-center gap-2">
					<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium text-white {config.bgClass}">
						{#if task.status === 'processing'}
							<Zap class="h-3 w-3" />
						{:else if task.status === 'blocked'}
							<AlertCircle class="h-3 w-3" />
						{:else if task.status === 'done'}
							<CheckCircle class="h-3 w-3" />
						{:else}
							<Clock class="h-3 w-3" />
						{/if}
						{config.label}
					</span>
					{#if task.project && task.project !== 'personal'}
						<span class="text-xs px-2 py-0.5 rounded-full border border-input">{task.project}</span>
					{/if}
					{#if task.type}
						<span class="text-xs px-2 py-0.5 rounded-full bg-secondary text-secondary-foreground">{task.type}</span>
					{/if}
					{#if task.dangerous_mode}
						<span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-destructive text-destructive-foreground">
							<ShieldAlert class="h-3 w-3" />
							Dangerous
						</span>
					{/if}
				</div>
			</div>

			<div class="flex items-center gap-1">
				{#if !isEditing}
					<button class="p-2 rounded-lg hover:bg-muted" onclick={() => (isEditing = true)}>
						<Edit3 class="h-4 w-4" />
					</button>
				{/if}
				<button class="p-2 rounded-lg hover:bg-muted" onclick={onClose}>
					<X class="h-4 w-4" />
				</button>
			</div>
		</div>

		<!-- Body -->
		<div class="flex-1 overflow-y-auto scrollbar-thin">
			<!-- Description -->
			<div class="p-5 border-b border-border">
				{#if isEditing}
					<textarea
						bind:value={editBody}
						placeholder="Task description..."
						rows="4"
						class="w-full px-3 py-2 rounded-lg border border-input bg-background font-mono text-sm resize-none"
					></textarea>
					<div class="flex justify-end gap-2 mt-3">
						<button class="px-3 py-1.5 rounded-md text-sm hover:bg-muted" onclick={() => (isEditing = false)}>Cancel</button>
						<button
							class="inline-flex items-center gap-1 px-3 py-1.5 rounded-md text-sm bg-primary text-primary-foreground"
							disabled={actionLoading === 'save' || !editTitle.trim()}
							onclick={handleSaveEdit}
						>
							<Save class="h-3.5 w-3.5" />
							Save
						</button>
					</div>
				{:else if task.body}
					<pre class="text-sm text-muted-foreground whitespace-pre-wrap font-mono">{task.body}</pre>
				{:else}
					<p class="text-sm text-muted-foreground italic">No description</p>
				{/if}
			</div>

			<!-- Metadata -->
			<div class="p-5 border-b border-border">
				<div class="grid grid-cols-2 gap-4 text-sm">
					<div class="flex items-center gap-2 text-muted-foreground">
						<Calendar class="h-4 w-4" />
						<span>Created {formatDate(task.created_at)}</span>
					</div>
					{#if task.started_at}
						<div class="flex items-center gap-2 text-muted-foreground">
							<Clock class="h-4 w-4" />
							<span>
								{task.completed_at
									? `Took ${formatDuration(task.started_at, task.completed_at)}`
									: `Running for ${formatDuration(task.started_at)}`
								}
							</span>
						</div>
					{/if}
					{#if task.branch_name}
						<div class="flex items-center gap-2 text-muted-foreground col-span-2">
							<GitBranch class="h-4 w-4" />
							<code class="text-xs bg-muted px-2 py-0.5 rounded">{task.branch_name}</code>
						</div>
					{/if}
					{#if task.pr_url}
						<div class="flex items-center gap-2 col-span-2">
							<GitPullRequest class="h-4 w-4 text-muted-foreground" />
							<a
								href={task.pr_url}
								target="_blank"
								rel="noopener noreferrer"
								class="text-sm text-blue-500 hover:underline inline-flex items-center gap-1"
							>
								Pull Request #{task.pr_number}
								<ExternalLink class="h-3 w-3" />
							</a>
						</div>
					{/if}
				</div>
			</div>

			<!-- Actions -->
			<div class="p-5 border-b border-border">
				<div class="flex flex-wrap gap-2">
					{#if task.status === 'backlog'}
						<button
							class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground font-medium"
							disabled={actionLoading !== null}
							onclick={handleQueue}
						>
							<Play class="h-4 w-4" />
							{actionLoading === 'queue' ? 'Starting...' : 'Run Task'}
						</button>
					{/if}

					{#if task.status === 'blocked'}
						<button
							class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-orange-500 text-white font-medium"
							disabled={actionLoading !== null}
							onclick={() => (showRetryDialog = true)}
						>
							<RotateCcw class="h-4 w-4" />
							Retry with Feedback
						</button>
					{/if}

					{#if task.status === 'done'}
						<button
							class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-secondary text-secondary-foreground font-medium"
							disabled={actionLoading !== null}
							onclick={() => (showRetryDialog = true)}
						>
							<RotateCcw class="h-4 w-4" />
							Run Again
						</button>
					{/if}

					{#if task.status === 'processing' || task.status === 'blocked' || task.status === 'queued'}
						<button
							class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-secondary text-secondary-foreground font-medium"
							disabled={actionLoading !== null}
							onclick={handleClose}
						>
							<CheckCircle class="h-4 w-4" />
							{actionLoading === 'close' ? 'Closing...' : 'Mark Done'}
						</button>
					{/if}

					<button
						class="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-destructive hover:bg-destructive/10 font-medium ml-auto"
						disabled={actionLoading !== null}
						onclick={handleDelete}
					>
						<Trash2 class="h-4 w-4" />
						Delete
					</button>
				</div>
			</div>

			<!-- Logs Section -->
			<div class="flex flex-col">
				<button
					class="flex items-center justify-between px-5 py-3 hover:bg-muted/50 transition-colors"
					onclick={() => (logsExpanded = !logsExpanded)}
				>
					<div class="flex items-center gap-2">
						<Terminal class="h-4 w-4 text-muted-foreground" />
						<span class="font-medium text-sm">Execution Logs</span>
						{#if logs.length > 0}
							<span class="text-xs px-2 py-0.5 rounded-full bg-secondary text-secondary-foreground">{logs.length}</span>
						{/if}
					</div>
					{#if logsExpanded}
						<ChevronUp class="h-4 w-4 text-muted-foreground" />
					{:else}
						<ChevronDown class="h-4 w-4 text-muted-foreground" />
					{/if}
				</button>

				{#if logsExpanded}
					<div class="bg-gray-950 p-4 font-mono text-xs max-h-[400px] overflow-y-auto scrollbar-thin">
						{#if loading}
							<div class="text-muted-foreground animate-pulse">Loading logs...</div>
						{:else if logs.length === 0}
							<div class="text-muted-foreground">No logs yet. Run the task to see output.</div>
						{:else}
							{#each logs as log}
								<div class="py-0.5 leading-relaxed {logTypeColors[log.line_type] || 'text-gray-300'}">
									{log.content}
								</div>
							{/each}
						{/if}
						<div bind:this={logsEndEl}></div>
					</div>
				{/if}
			</div>
		</div>

		<!-- Retry Dialog -->
		{#if showRetryDialog}
			<div class="absolute inset-0 bg-black/50 flex items-center justify-center p-6 z-10">
				<div class="bg-card rounded-lg shadow-xl w-full max-w-md p-6">
					<div class="flex items-center gap-2 mb-4">
						<MessageSquare class="h-5 w-5 text-primary" />
						<h3 class="font-semibold">Add Feedback</h3>
					</div>
					<p class="text-sm text-muted-foreground mb-4">
						Provide additional context or instructions for the AI to consider when retrying this task.
					</p>
					<textarea
						bind:value={retryFeedback}
						placeholder="e.g., 'Please also update the tests' or 'Use the existing helper function instead'"
						rows="4"
						class="w-full px-3 py-2 rounded-lg border border-input bg-background text-sm resize-none mb-4"
					></textarea>
					<div class="flex justify-end gap-2">
						<button
							class="px-3 py-1.5 rounded-md text-sm hover:bg-muted"
							onclick={() => { showRetryDialog = false; retryFeedback = ''; }}
						>
							Cancel
						</button>
						<button
							class="px-4 py-1.5 rounded-md text-sm bg-primary text-primary-foreground"
							disabled={actionLoading === 'retry'}
							onclick={handleRetry}
						>
							{actionLoading === 'retry' ? 'Retrying...' : 'Retry Task'}
						</button>
					</div>
				</div>
			</div>
		{/if}
	</div>
</div>
