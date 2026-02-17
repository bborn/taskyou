<script lang="ts">
	import { X, Trash2 } from 'lucide-svelte';
	import type { Project } from '$lib/types';

	interface Props {
		project?: Project;
		onSubmit: (data: { name: string; instructions?: string; color?: string }) => Promise<void>;
		onDelete?: () => Promise<void>;
		onClose: () => void;
	}

	let { project, onSubmit, onDelete, onClose }: Props = $props();

	let name = $state(project?.name || '');
	let instructions = $state(project?.instructions || '');
	let color = $state(project?.color || '#888888');
	let submitting = $state(false);
	let dialogEl: HTMLDialogElement;

	const isEditing = !!project;

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
		if (!name.trim()) return;

		submitting = true;
		try {
			await onSubmit({ name: name.trim(), instructions: instructions.trim(), color });
			dialogEl.close();
		} catch (err) {
			console.error(err);
		} finally {
			submitting = false;
		}
	}

	async function handleDelete() {
		if (!confirm('Delete this project?')) return;
		try {
			await onDelete?.();
			dialogEl.close();
		} catch (err) {
			console.error(err);
		}
	}
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog
	bind:this={dialogEl}
	class="dialog w-full sm:max-w-[425px]"
	aria-labelledby="project-dialog-title"
	onclose={handleDialogClose}
	onclick={handleBackdropClick}
>
	<form onsubmit={handleSubmit}>
		<header>
			<h2 id="project-dialog-title">{isEditing ? 'Edit Project' : 'New Project'}</h2>
		</header>

		<section>
			<div class="form grid gap-4">
				<div class="grid gap-2">
					<label for="project-name">Name</label>
					<input id="project-name" bind:value={name} placeholder="Project name" autofocus />
				</div>
				<div class="grid gap-2">
					<label for="project-instructions">Instructions</label>
					<textarea
						id="project-instructions"
						bind:value={instructions}
						placeholder="Default instructions for AI when working on this project..."
						rows="3"
					></textarea>
				</div>
				<div class="grid gap-2">
					<label for="project-color">Color</label>
					<input id="project-color" bind:value={color} type="color" class="h-10 w-20 rounded border border-input cursor-pointer" />
				</div>
			</div>
		</section>

		<footer>
			{#if isEditing && onDelete}
				<button type="button" class="btn-destructive sm:mr-auto" onclick={handleDelete}>
					<Trash2 class="h-3.5 w-3.5" />
					Delete
				</button>
			{/if}
			<button type="button" class="btn-outline" onclick={() => dialogEl.close()}>Cancel</button>
			<button type="submit" class="btn" disabled={!name.trim() || submitting}>
				{submitting ? 'Saving...' : isEditing ? 'Update' : 'Create'}
			</button>
		</footer>

		<button type="button" aria-label="Close dialog" title="Close (Esc)" onclick={() => dialogEl.close()}>
			<X class="h-4 w-4" />
		</button>
	</form>
</dialog>
