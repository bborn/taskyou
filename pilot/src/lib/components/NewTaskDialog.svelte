<script lang="ts">
	import { X, Tag, FileText, Folder } from 'lucide-svelte';
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
	let dialogEl: HTMLDialogElement;

	const taskTypes = [
		{ value: 'code', label: 'Code', description: 'Write or modify code' },
		{ value: 'writing', label: 'Writing', description: 'Documentation, content' },
		{ value: 'thinking', label: 'Thinking', description: 'Research, analysis' },
	];

	$effect(() => {
		if (dialogEl && !dialogEl.open) {
			dialogEl.showModal();
		}
	});

	function handleDialogClose() {
		onClose();
	}

	function handleBackdropClick(e: MouseEvent) {
		if (e.target === dialogEl) dialogEl.close();
	}

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		if (!title.trim()) return;

		submitting = true;
		try {
			await onSubmit({
				title: title.trim(),
				body: body.trim() || undefined,
				type,
			});
			dialogEl.close();
		} catch (error) {
			console.error('Failed to create task:', error);
		} finally {
			submitting = false;
		}
	}
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog
	bind:this={dialogEl}
	class="dialog w-full sm:max-w-lg"
	aria-labelledby="new-task-title"
	aria-describedby="new-task-desc"
	onclose={handleDialogClose}
	onclick={handleBackdropClick}
>
	<form onsubmit={handleSubmit}>
		<header>
			<h2 id="new-task-title">New Task</h2>
			<p id="new-task-desc">What would you like AI to do?</p>
		</header>

		<section>
			<div class="form grid gap-4">
				<!-- Title -->
				<div class="grid gap-2">
					<label for="task-title" class="flex items-center gap-2">
						<FileText class="h-4 w-4 text-muted-foreground" />
						What needs to be done?
					</label>
					<input
						id="task-title"
						bind:value={title}
						placeholder="e.g., Fix the login bug, Add dark mode support..."
						autofocus
					/>
				</div>

				<!-- Description -->
				<div class="grid gap-2">
					<label for="task-body" class="text-muted-foreground">Additional details (optional)</label>
					<textarea
						id="task-body"
						bind:value={body}
						placeholder="Provide context, requirements, or specific instructions..."
						rows="3"
					></textarea>
				</div>

				<!-- Type Selection -->
				<div class="grid gap-2">
					<label class="flex items-center gap-2">
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
				<div class="grid gap-2">
					<label for="task-project" class="flex items-center gap-2">
						<Folder class="h-4 w-4 text-muted-foreground" />
						Project
					</label>
					<select id="task-project" bind:value={project}>
						<option value="personal">Personal</option>
						{#each projects as p}
							<option value={p.name}>{p.name}</option>
						{/each}
					</select>
				</div>
			</div>
		</section>

		<footer>
			<button type="button" class="btn-outline" onclick={() => dialogEl.close()}>Cancel</button>
			<button type="submit" class="btn" disabled={!title.trim() || submitting}>
				{#if submitting}
					Creating...
				{:else}
					Create Task
				{/if}
			</button>
		</footer>

		<button type="button" aria-label="Close dialog" title="Close (Esc)" onclick={() => dialogEl.close()}>
			<X class="h-4 w-4" />
		</button>
	</form>
</dialog>
