<script lang="ts">
	import { X, Sparkles, Folder, Tag, FileText } from 'lucide-svelte';
	import type { CreateTaskRequest, Project } from '$lib/types';

	interface Props {
		projects: Project[];
		onSubmit: (data: CreateTaskRequest) => Promise<void>;
		onClose: () => void;
	}

	let { projects, onSubmit, onClose }: Props = $props();

	let title = $state('');
	let body = $state('');
	let type = $state('code');
	let project = $state('personal');
	let submitting = $state(false);

	const taskTypes = [
		{ value: 'code', label: 'Code', description: 'Write or modify code' },
		{ value: 'writing', label: 'Writing', description: 'Documentation, content' },
		{ value: 'thinking', label: 'Thinking', description: 'Research, analysis' },
	];

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		if (!title.trim()) return;

		submitting = true;
		try {
			await onSubmit({
				title: title.trim(),
				body: body.trim() || undefined,
				type,
				project,
			});
			onClose();
		} catch (error) {
			console.error('Failed to create task:', error);
		} finally {
			submitting = false;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') onClose();
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="fixed inset-0 z-50 flex items-center justify-center p-4" onkeydown={handleKeydown}>
	<!-- Backdrop -->
	<div class="absolute inset-0 bg-black/60 backdrop-blur-sm" onclick={onClose}></div>

	<!-- Dialog -->
	<div class="relative w-full max-w-lg bg-card rounded-xl shadow-2xl border border-border overflow-hidden">
		<form onsubmit={handleSubmit}>
			<!-- Header -->
			<div class="flex items-center justify-between px-6 py-4 border-b border-border">
				<div class="flex items-center gap-2">
					<div class="p-2 rounded-lg bg-primary/10">
						<Sparkles class="h-5 w-5 text-primary" />
					</div>
					<div>
						<h2 class="font-semibold">New Task</h2>
						<p class="text-xs text-muted-foreground">What would you like AI to do?</p>
					</div>
				</div>
				<button type="button" class="p-2 rounded-lg hover:bg-muted" onclick={onClose}>
					<X class="h-4 w-4" />
				</button>
			</div>

			<!-- Content -->
			<div class="p-6 space-y-5">
				<!-- Title -->
				<div>
					<label class="flex items-center gap-2 text-sm font-medium mb-2">
						<FileText class="h-4 w-4 text-muted-foreground" />
						What needs to be done?
					</label>
					<input
						bind:value={title}
						placeholder="e.g., Fix the login bug, Add dark mode support..."
						class="w-full h-10 px-3 rounded-lg border border-input bg-background text-base focus:outline-none focus:ring-2 focus:ring-ring"
						autofocus
					/>
				</div>

				<!-- Description -->
				<div>
					<label class="text-sm font-medium mb-2 block text-muted-foreground">
						Additional details (optional)
					</label>
					<textarea
						bind:value={body}
						placeholder="Provide context, requirements, or specific instructions..."
						rows="3"
						class="w-full px-3 py-2 rounded-lg border border-input bg-background text-sm resize-none focus:outline-none focus:ring-2 focus:ring-ring"
					></textarea>
				</div>

				<!-- Type Selection -->
				<div>
					<label class="flex items-center gap-2 text-sm font-medium mb-2">
						<Tag class="h-4 w-4 text-muted-foreground" />
						Task type
					</label>
					<div class="grid grid-cols-3 gap-2">
						{#each taskTypes as t}
							<button
								type="button"
								onclick={() => (type = t.value)}
								class="flex flex-col items-center gap-1 p-3 rounded-lg border-2 transition-all {type === t.value ? 'border-primary bg-primary/5 text-primary' : 'border-transparent bg-muted/50 text-muted-foreground hover:bg-muted'}"
							>
								<span class="font-medium text-sm">{t.label}</span>
								<span class="text-[10px] opacity-70">{t.description}</span>
							</button>
						{/each}
					</div>
				</div>

				<!-- Project Selection -->
				<div>
					<label class="flex items-center gap-2 text-sm font-medium mb-2">
						<Folder class="h-4 w-4 text-muted-foreground" />
						Project
					</label>
					<select
						bind:value={project}
						class="w-full h-10 rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
					>
						<option value="personal">Personal</option>
						{#each projects as p}
							<option value={p.name}>{p.name}</option>
						{/each}
					</select>
				</div>
			</div>

			<!-- Footer -->
			<div class="flex items-center justify-end px-6 py-4 border-t border-border bg-muted/30 gap-2">
				<button type="button" class="px-3 py-2 rounded-md text-sm hover:bg-muted" onclick={onClose}>
					Cancel
				</button>
				<button
					type="submit"
					disabled={!title.trim() || submitting}
					class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground font-medium min-w-[100px] disabled:opacity-50"
				>
					{#if submitting}
						<span class="h-4 w-4 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
						Creating...
					{:else}
						Create Task
					{/if}
				</button>
			</div>
		</form>
	</div>
</div>
