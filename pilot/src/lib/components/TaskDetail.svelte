<script lang="ts">
	import { onMount } from 'svelte';
	import {
		X, Play, RotateCcw, CheckCircle, Trash2, Edit3, Save,
		Zap, AlertCircle, ExternalLink, XCircle, FileText,
		Loader2,
	} from 'lucide-svelte';
	import { tasks as tasksApi } from '$lib/api/client';
	import { updateTask as storeUpdateTask } from '$lib/stores/tasks.svelte';
	import { Marked } from 'marked';
	import type { Task, TaskLog, TaskStatus } from '$lib/types';

	interface Props {
		task: Task;
		onClose: () => void;
		onUpdate: (task: Task) => void;
		onDelete?: () => void;
	}

	let { task: initialTask, onClose, onUpdate, onDelete }: Props = $props();

	const marked = new Marked({ breaks: true, gfm: true });

	let task = $state(initialTask);
	let logs = $state<TaskLog[]>([]);
	let loading = $state(false);
	let actionLoading = $state<string | null>(null);
	let isEditing = $state(false);
	let editTitle = $state(task.title);
	let editBody = $state(task.body || '');
	let showRetryDialog = $state(false);
	let retryFeedback = $state('');
	let logsEndEl: HTMLDivElement;
	let dialogEl: HTMLDialogElement;
	let retryDialogEl: HTMLDialogElement;
	let pollInterval: ReturnType<typeof setInterval> | null = null;

	// File viewer state
	interface FileEntry {
		path: string;
		changes: string;
		additions: number;
		deletions: number;
	}

	let fileEntries = $derived.by<FileEntry[]>(() => {
		if (!task.summary) return [];
		return task.summary.split('\n').reduce<FileEntry[]>((acc, line) => {
			let match = line.match(/^\s*(.+?)\s+\|\s+(\d+)\s*([+\-]*)/);
			if (match) {
				acc.push({ path: match[1].trim(), changes: match[2], additions: (match[3] || '').split('').filter(c => c === '+').length, deletions: (match[3] || '').split('').filter(c => c === '-').length });
			} else {
				match = line.match(/^\s*(.+?)\s+\|\s+Bin/);
				if (match) acc.push({ path: match[1].trim(), changes: 'bin', additions: 0, deletions: 0 });
			}
			return acc;
		}, []);
	});

	let hasFiles = $derived(fileEntries.length > 0);
	let selectedFile = $state<string | null>(null);
	let fileViewMode = $state<'preview' | 'source' | 'diff'>('preview');
	let fileContent = $state<string>('');
	let fileLoading = $state(false);
	let fileCache = new Map<string, string>();

	// Auto-select first file
	$effect(() => {
		if (hasFiles && !selectedFile) {
			selectedFile = fileEntries[0].path;
		}
	});

	// Load file content when selection or mode changes
	$effect(() => {
		if (selectedFile && task.id) {
			loadFileContent(task.id, selectedFile, fileViewMode);
		}
	});

	async function loadFileContent(taskId: number, path: string, mode: string) {
		const cacheKey = `${taskId}:${path}`;
		fileLoading = true;
		try {
			let content: string;
			if (fileCache.has(cacheKey)) {
				content = fileCache.get(cacheKey)!;
			} else {
				const res = await fetch(`/api/tasks/${taskId}/file?path=${encodeURIComponent(path)}`);
				if (!res.ok) {
					content = `Error loading file: ${res.statusText}`;
				} else {
					content = await res.text();
				}
				fileCache.set(cacheKey, content);
			}
			fileContent = content;
		} catch (e) {
			fileContent = `Error: ${e instanceof Error ? e.message : 'Failed to load file'}`;
		} finally {
			fileLoading = false;
		}
	}

	$effect(() => {
		task = initialTask;
		editTitle = initialTask.title;
		editBody = initialTask.body || '';
	});

	const statusConfig: Record<TaskStatus, { label: string }> = {
		backlog: { label: 'Backlog' },
		queued: { label: 'Queued' },
		processing: { label: 'Running' },
		blocked: { label: 'Blocked' },
		done: { label: 'Done' },
		failed: { label: 'Failed' },
	};

	const logTypeColors: Record<string, string> = {
		error: 'text-red-400',
		system: 'text-yellow-400',
		tool: 'text-cyan-400',
		output: 'text-green-400',
		text: 'text-gray-300',
	};

	let config = $derived(statusConfig[task.status]);
	let subtasks = $derived.by<{ title: string; done: boolean }[]>(() => {
		if (!task.subtasks_json) return [];
		try { return JSON.parse(task.subtasks_json); } catch { return []; }
	});

	let isRunning = $derived(task.status === 'processing' || task.status === 'queued');

	async function fetchLogs() {
		try {
			const taskLogs = await tasksApi.getLogs(task.id, 200);
			logs = taskLogs.reverse();
		} catch (err) {
			console.error('Failed to fetch logs:', err);
		}
	}

	$effect(() => {
		if (dialogEl && !dialogEl.open) dialogEl.showModal();
	});

	onMount(() => {
		loading = true;
		fetchLogs().then(() => { loading = false; });

		if (isRunning) {
			pollInterval = setInterval(fetchLogs, 1500);
		}

		return () => {
			if (pollInterval) clearInterval(pollInterval);
		};
	});

	$effect(() => {
		if (logsEndEl) logsEndEl.scrollIntoView({ behavior: 'smooth' });
	});

	$effect(() => {
		if (showRetryDialog && retryDialogEl) retryDialogEl.showModal();
	});

	function handleDialogClose() { onClose(); }
	function handleBackdropClick(e: MouseEvent) { if (e.target === dialogEl) dialogEl.close(); }
	function handleRetryBackdropClick(e: MouseEvent) {
		if (e.target === retryDialogEl) { retryDialogEl.close(); showRetryDialog = false; retryFeedback = ''; }
	}
	function closeDialog() { dialogEl.close(); }

	async function handleQueue() {
		actionLoading = 'queue';
		try { const updated = await storeUpdateTask(task.id, { status: 'queued' }); task = updated; onUpdate(updated); }
		catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleRetry() {
		actionLoading = 'retry';
		try {
			const updated = await storeUpdateTask(task.id, { status: 'queued' });
			task = updated; onUpdate(updated);
			retryDialogEl.close(); showRetryDialog = false; retryFeedback = '';
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleMarkDone() {
		actionLoading = 'close';
		try { const updated = await storeUpdateTask(task.id, { status: 'done' }); task = updated; onUpdate(updated); }
		catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleDelete() {
		if (!confirm('Are you sure you want to delete this task?')) return;
		actionLoading = 'delete';
		try { await tasksApi.delete(task.id); onDelete?.(); closeDialog(); }
		catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

	async function handleSaveEdit() {
		if (!editTitle.trim()) return;
		actionLoading = 'save';
		try {
			const updated = await tasksApi.update(task.id, { title: editTitle.trim(), body: editBody.trim() });
			task = updated; onUpdate(updated); isEditing = false;
		} catch (err) { console.error(err); }
		finally { actionLoading = null; }
	}

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

	function renderMarkdown(content: string): string {
		try { return marked.parse(content) as string; } catch { return content; }
	}

	function fileExtension(path: string): string {
		const dot = path.lastIndexOf('.');
		return dot >= 0 ? path.slice(dot).toLowerCase() : '';
	}

	function fileName(path: string): string {
		return path.split('/').pop() || path;
	}

	function renderDiffHtml(content: string): string {
		return content.split('\n').map(line => {
			if (line.startsWith('+') && !line.startsWith('+++')) return `<div class="bg-green-500/10 text-green-700 dark:text-green-300 px-3">${escapeHtml(line)}</div>`;
			if (line.startsWith('-') && !line.startsWith('---')) return `<div class="bg-red-500/10 text-red-700 dark:text-red-300 px-3">${escapeHtml(line)}</div>`;
			if (line.startsWith('@@')) return `<div class="text-blue-500 dark:text-blue-400 px-3">${escapeHtml(line)}</div>`;
			return `<div class="px-3">${escapeHtml(line)}</div>`;
		}).join('');
	}

	function renderSourceHtml(content: string): string {
		const lines = content.split('\n');
		return lines.map((line, i) =>
			`<tr><td class="select-none text-right pr-3 text-muted-foreground/40 w-[1%] whitespace-nowrap">${i + 1}</td><td class="break-all">${escapeHtml(line)}</td></tr>`
		).join('');
	}

	function escapeHtml(s: string): string {
		return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
	}
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog
	bind:this={dialogEl}
	class="dialog"
	style={hasFiles ? 'width: 90vw; height: 85vh;' : 'width: fit-content; min-width: 32rem; max-width: 48rem;'}
	aria-labelledby="task-detail-title"
	onclose={handleDialogClose}
	onclick={handleBackdropClick}
>
	<div style={hasFiles ? 'width: 90vw; max-width: 90vw; height: 85vh; max-height: 85vh; padding-bottom: 0;' : ''}>
		<!-- Header -->
		<header>
			<h2 id="task-detail-title">{task.title}</h2>
			<div class="flex flex-wrap items-center gap-2 mt-1">
				<span class="badge text-white text-xs" style="background-color: var(--status-{task.status});">{config.label}</span>
				{#if task.type}
					<span class="badge-outline text-xs">{task.type}</span>
				{/if}
					<span class="text-xs text-muted-foreground">#{task.id}</span>
				{#if isRunning}
					<Loader2 class="size-4 animate-spin text-primary" />
				{/if}
				{#if task.status === 'backlog' || task.status === 'blocked' || task.status === 'failed'}
					<button
						class="btn h-5 px-1.5 gap-1 text-[10px] inline-flex items-center"
						disabled={actionLoading !== null}
						onclick={handleQueue}
						title="Execute task (⌘↵)"
					>
						<Play class="size-2.5" />
						Execute
					</button>
				{/if}
				{#if task.status === 'blocked' || task.status === 'failed'}
					<button
						class="btn-outline h-5 px-1.5 gap-1 text-[10px] inline-flex items-center"
						disabled={actionLoading !== null}
						onclick={() => (showRetryDialog = true)}
					>
						<RotateCcw class="size-2.5" />
						Retry
					</button>
				{/if}
				{#if isRunning}
					<button
						class="btn-outline h-5 px-1.5 gap-1 text-[10px] text-amber-600 dark:text-amber-400 border-amber-500/30 hover:bg-amber-500/10 inline-flex items-center"
						disabled={actionLoading !== null}
						onclick={handleMarkDone}
					>
						<CheckCircle class="size-2.5" />
						Done
					</button>
				{/if}
				{#if !isEditing}
					<button
						class="inline-flex items-center justify-center h-5 w-5 rounded text-muted-foreground/50 hover:text-destructive hover:bg-destructive/10"
						onclick={handleDelete}
						disabled={actionLoading !== null}
						title="Delete task"
					>
						<Trash2 class="size-3" />
					</button>
				{/if}
				<span class="text-[10px] text-muted-foreground/60">&middot; {timeAgo(task.created_at)}</span>
				{#if task.started_at}
					<span class="text-[10px] text-muted-foreground/60">&middot; started {timeAgo(task.started_at)}</span>
				{/if}
				{#if task.completed_at}
					<span class="text-[10px] text-muted-foreground/60">&middot; completed {timeAgo(task.completed_at)}</span>
				{/if}
			</div>
		</header>

		<!-- Content -->
		<section class="!p-0 overflow-hidden !flex-1">
			{#if hasFiles}
				<!-- Two-pane layout for tasks with files -->
				<div class="flex h-full">
					<!-- Left pane: task info -->
					<div class="w-1/3 shrink-0 border-r border-border overflow-y-auto p-4 space-y-3">
						{#if isEditing}
							<div class="space-y-2">
								<input bind:value={editTitle} class="input w-full text-sm font-semibold" />
								<textarea bind:value={editBody} placeholder="Task description..." rows="4" class="textarea w-full font-mono text-sm"></textarea>
								<div class="flex justify-end gap-2">
									<button class="btn-sm-ghost" onclick={() => (isEditing = false)}>Cancel</button>
									<button class="btn-sm" disabled={actionLoading === 'save' || !editTitle.trim()} onclick={handleSaveEdit}>
										<Save class="h-3.5 w-3.5" /> Save
									</button>
								</div>
							</div>
						{:else}
							{#if task.body}
								<div class="bg-muted rounded-lg p-3">
									<div class="text-sm">{@html renderMarkdown(task.body)}</div>
								</div>
							{/if}
							<button class="text-[10px] text-muted-foreground hover:text-foreground" onclick={() => (isEditing = true)}>
								<Edit3 class="size-3 inline" /> Edit
							</button>
						{/if}

						{#if subtasks.length > 0}
							<div>
								<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">Subtasks</p>
								<div class="space-y-1">
									{#each subtasks as subtask}
										<div class="flex items-center gap-2 text-xs px-2 py-1 rounded bg-muted">
											{#if subtask.done}
												<CheckCircle class="size-3 text-green-500 shrink-0" />
											{:else}
												<div class="size-3 rounded-full border-2 border-muted-foreground/30 shrink-0"></div>
											{/if}
											<span class="flex-1 truncate" class:line-through={subtask.done} class:text-muted-foreground={subtask.done}>{subtask.title}</span>
										</div>
									{/each}
								</div>
							</div>
						{/if}

						{#if task.output}
							<div>
								<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">Summary</p>
								<div class="bg-muted rounded-lg p-3 text-sm chat-markdown">
									{@html renderMarkdown(task.output)}
								</div>
							</div>
						{/if}

						{#if isRunning}
							<div>
								<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">Live Output</p>
								<div class="bg-gray-950 rounded-lg p-3 max-h-48 overflow-y-auto font-mono text-xs">
									{#if logs.length === 0}
										<p class="text-muted-foreground">Waiting for output...</p>
									{:else}
										{#each logs.slice(-20) as log}
											<div class="{logTypeColors[log.line_type] || 'text-gray-300'}">{log.content}</div>
										{/each}
									{/if}
								</div>
							</div>
						{/if}

					</div>

					<!-- Right pane: file viewer -->
					<div class="flex-1 flex flex-col min-w-0">
						<!-- File tabs + view mode -->
						<div class="flex items-end shrink-0 bg-muted/40 border-b border-border">
							<div class="flex items-end gap-0 px-2 pt-2 overflow-x-auto flex-1 min-w-0">
								{#each fileEntries as entry}
									<button
										class="relative px-3 py-1.5 text-[11px] font-medium whitespace-nowrap rounded-t-md transition-all {selectedFile === entry.path ? 'bg-background text-foreground shadow-sm z-10 border border-border border-b-background -mb-px' : 'text-muted-foreground hover:text-foreground hover:bg-muted/60'}"
										onclick={() => { selectedFile = entry.path; fileViewMode = 'preview'; }}
										title={entry.path}
									>
										<span class="flex items-center gap-1.5">
											<FileText class="size-3 shrink-0 text-muted-foreground" />
											{fileName(entry.path)}
										</span>
									</button>
								{/each}
							</div>
							<div class="flex gap-0.5 bg-muted/60 rounded-md p-0.5 shrink-0 mr-2 mb-1.5">
								{#each ['preview', 'source', 'diff'] as mode}
									<button
										class="px-2 py-0.5 text-[10px] rounded font-medium {fileViewMode === mode ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
										onclick={() => (fileViewMode = mode as typeof fileViewMode)}
									>
										{mode === 'preview' ? 'Preview' : mode === 'source' ? 'Source' : 'Changes'}
									</button>
								{/each}
							</div>
						</div>

						<!-- File content -->
						<div class="flex-1 overflow-auto bg-background">
							{#if fileLoading}
								<div class="flex items-center justify-center py-12">
									<Loader2 class="size-5 animate-spin text-muted-foreground" />
								</div>
							{:else if fileViewMode === 'diff'}
								<pre class="text-xs font-mono leading-relaxed">{@html renderDiffHtml(fileContent)}</pre>
							{:else if fileViewMode === 'source'}
								<table class="text-xs font-mono leading-relaxed w-full">
									<tbody>{@html renderSourceHtml(fileContent)}</tbody>
								</table>
							{:else}
								{@const ext = selectedFile ? fileExtension(selectedFile) : ''}
								{#if ext === '.md' || ext === '.markdown'}
									<div class="p-4 text-sm prose prose-sm dark:prose-invert max-w-none chat-markdown">
										{@html renderMarkdown(fileContent)}
									</div>
								{:else if ext === '.html' || ext === '.htm'}
									<iframe
										srcdoc={fileContent}
										sandbox="allow-same-origin"
										class="w-full h-full border-0"
										title="HTML Preview"
									></iframe>
								{:else if ext === '.json'}
									<pre class="p-4 text-xs font-mono whitespace-pre-wrap">{(() => { try { return JSON.stringify(JSON.parse(fileContent), null, 2); } catch { return fileContent; } })()}</pre>
								{:else}
									<table class="text-xs font-mono leading-relaxed w-full">
										<tbody>{@html renderSourceHtml(fileContent)}</tbody>
									</table>
								{/if}
							{/if}
						</div>
					</div>
				</div>
			{:else}
				<!-- Single-column layout for tasks without files -->
				<div class="space-y-3 overflow-y-auto p-4">
					{#if isEditing}
						<div class="space-y-2">
							<input bind:value={editTitle} class="input w-full text-sm font-semibold" />
							<textarea bind:value={editBody} placeholder="Task description..." rows="6" class="textarea w-full font-mono text-sm"></textarea>
							<div class="flex justify-end gap-2">
								<button class="btn-sm-ghost" onclick={() => (isEditing = false)}>Cancel</button>
								<button class="btn-sm" disabled={actionLoading === 'save' || !editTitle.trim()} onclick={handleSaveEdit}>
									<Save class="h-3.5 w-3.5" /> Save
								</button>
							</div>
						</div>
					{:else}
						{#if task.body}
							<div class="bg-muted rounded-lg p-3">
								<div class="text-sm">{@html renderMarkdown(task.body)}</div>
							</div>
						{/if}
						<button class="text-[10px] text-muted-foreground hover:text-foreground" onclick={() => (isEditing = true)}>
							<Edit3 class="size-3 inline" /> Edit
						</button>
					{/if}

					{#if subtasks.length > 0}
						<div>
							<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-2">Subtasks</p>
							<div class="space-y-1">
								{#each subtasks as subtask}
									<div class="flex items-center gap-2 text-xs px-2 py-1.5 rounded bg-muted">
										{#if subtask.done}
											<CheckCircle class="size-3 text-green-500 shrink-0" />
										{:else}
											<div class="size-3 rounded-full border-2 border-muted-foreground/30 shrink-0"></div>
										{/if}
										<span class="flex-1 truncate" class:line-through={subtask.done} class:text-muted-foreground={subtask.done}>{subtask.title}</span>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					{#if task.output}
						<div>
							<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-2">Summary</p>
							<div class="bg-muted rounded-lg p-3 max-h-64 overflow-y-auto text-sm chat-markdown">
								{@html renderMarkdown(task.output)}
							</div>
						</div>
					{/if}

					{#if task.summary}
						<div>
							<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-2">Files Changed</p>
							<div class="bg-muted rounded-lg p-3 max-h-48 overflow-y-auto">
								<pre class="text-xs font-mono whitespace-pre-wrap">{task.summary}</pre>
							</div>
						</div>
					{/if}

					{#if isRunning}
						<div>
							<p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-2">Live Output</p>
							<div class="bg-gray-950 rounded-lg p-3 max-h-48 overflow-y-auto font-mono text-xs">
								{#if logs.length === 0}
									<p class="text-muted-foreground">Waiting for output...</p>
								{:else}
									{#each logs as log}
										<div class="{logTypeColors[log.line_type] || 'text-gray-300'}">{log.content}</div>
									{/each}
								{/if}
								<div bind:this={logsEndEl}></div>
							</div>
						</div>
					{/if}

				</div>
			{/if}
		</section>

		<button type="button" aria-label="Close dialog" title="Close (Esc)" onclick={closeDialog}>
			<X class="h-4 w-4" />
		</button>
	</div>
</dialog>

<!-- Retry Dialog -->
{#if showRetryDialog}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<dialog
		bind:this={retryDialogEl}
		class="dialog w-full sm:max-w-[425px]"
		aria-labelledby="retry-dialog-title"
		aria-describedby="retry-dialog-desc"
		onclose={() => { showRetryDialog = false; retryFeedback = ''; }}
		onclick={handleRetryBackdropClick}
	>
		<div>
			<header>
				<h2 id="retry-dialog-title">Add Feedback</h2>
				<p id="retry-dialog-desc">Provide additional context or instructions for the AI to consider when retrying this task.</p>
			</header>
			<section>
				<textarea bind:value={retryFeedback} placeholder="e.g., 'Please also update the tests' or 'Use the existing helper function instead'" rows="4" class="textarea w-full text-sm"></textarea>
			</section>
			<footer>
				<button class="btn-outline" onclick={() => retryDialogEl.close()}>Cancel</button>
				<button class="btn" disabled={actionLoading === 'retry'} onclick={handleRetry}>
					{actionLoading === 'retry' ? 'Retrying...' : 'Retry Task'}
				</button>
			</footer>
			<button type="button" aria-label="Close dialog" onclick={() => retryDialogEl.close()}>
				<X class="h-4 w-4" />
			</button>
		</div>
	</dialog>
{/if}
