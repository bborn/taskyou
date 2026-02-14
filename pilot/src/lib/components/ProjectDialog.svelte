<script lang="ts">
	import { X, Trash2 } from 'lucide-svelte';
	import type { Project } from '$lib/types';

	interface Props {
		project?: Project;
		onSubmit: (data: { name: string; path: string; aliases?: string; instructions?: string; color?: string }) => Promise<void>;
		onDelete?: () => Promise<void>;
		onClose: () => void;
	}

	let { project, onSubmit, onDelete, onClose }: Props = $props();

	let name = $state(project?.name || '');
	let path = $state(project?.path || '');
	let aliases = $state(project?.aliases || '');
	let instructions = $state(project?.instructions || '');
	let color = $state(project?.color || '#888888');
	let submitting = $state(false);

	const isEditing = !!project;

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		if (!name.trim()) return;

		submitting = true;
		try {
			await onSubmit({ name: name.trim(), path: path.trim(), aliases: aliases.trim(), instructions: instructions.trim(), color });
			onClose();
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
			onClose();
		} catch (err) {
			console.error(err);
		}
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
	<div class="absolute inset-0 bg-black/60 backdrop-blur-sm" onclick={onClose}></div>

	<div class="relative w-full max-w-md bg-card rounded-xl shadow-2xl border border-border overflow-hidden">
		<form onsubmit={handleSubmit}>
			<div class="flex items-center justify-between px-6 py-4 border-b border-border">
				<h2 class="font-semibold">{isEditing ? 'Edit Project' : 'New Project'}</h2>
				<button type="button" class="p-2 rounded-lg hover:bg-muted" onclick={onClose}>
					<X class="h-4 w-4" />
				</button>
			</div>

			<div class="p-6 space-y-4">
				<div>
					<label class="text-sm font-medium mb-1 block">Name</label>
					<input
						bind:value={name}
						placeholder="Project name"
						class="w-full h-10 px-3 rounded-lg border border-input bg-background text-sm"
						autofocus
					/>
				</div>
				<div>
					<label class="text-sm font-medium mb-1 block">Path</label>
					<input
						bind:value={path}
						placeholder="/path/to/project"
						class="w-full h-10 px-3 rounded-lg border border-input bg-background text-sm font-mono"
					/>
				</div>
				<div>
					<label class="text-sm font-medium mb-1 block">Aliases (comma-separated)</label>
					<input
						bind:value={aliases}
						placeholder="alias1, alias2"
						class="w-full h-10 px-3 rounded-lg border border-input bg-background text-sm"
					/>
				</div>
				<div>
					<label class="text-sm font-medium mb-1 block">Instructions</label>
					<textarea
						bind:value={instructions}
						placeholder="Default instructions for AI when working on this project..."
						rows="3"
						class="w-full px-3 py-2 rounded-lg border border-input bg-background text-sm resize-none"
					></textarea>
				</div>
				<div>
					<label class="text-sm font-medium mb-1 block">Color</label>
					<input bind:value={color} type="color" class="h-10 w-20 rounded border border-input cursor-pointer" />
				</div>
			</div>

			<div class="flex items-center justify-between px-6 py-4 border-t border-border bg-muted/30">
				{#if isEditing && onDelete}
					<button
						type="button"
						class="inline-flex items-center gap-1 px-3 py-1.5 rounded-md text-sm text-destructive hover:bg-destructive/10"
						onclick={handleDelete}
					>
						<Trash2 class="h-3.5 w-3.5" />
						Delete
					</button>
				{:else}
					<div></div>
				{/if}
				<div class="flex gap-2">
					<button type="button" class="px-3 py-1.5 rounded-md text-sm hover:bg-muted" onclick={onClose}>Cancel</button>
					<button
						type="submit"
						disabled={!name.trim() || submitting}
						class="px-4 py-1.5 rounded-md text-sm bg-primary text-primary-foreground disabled:opacity-50"
					>
						{submitting ? 'Saving...' : isEditing ? 'Update' : 'Create'}
					</button>
				</div>
			</div>
		</form>
	</div>
</div>
