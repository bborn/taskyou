<script lang="ts">
	import { X, Trash2, Github, Check, Loader2, ExternalLink } from 'lucide-svelte';
	import type { Project } from '$lib/types';
	import { github } from '$lib/api/client';

	interface Props {
		project?: Project;
		onSubmit: (data: { name: string; instructions?: string; color?: string; github_repo?: string; github_branch?: string }) => Promise<void>;
		onDelete?: () => Promise<void>;
		onClose: () => void;
	}

	let { project, onSubmit, onDelete, onClose }: Props = $props();

	let name = $state(project?.name || '');
	let instructions = $state(project?.instructions || '');
	let color = $state(project?.color || '#888888');
	let githubRepo = $state(project?.github_repo || '');
	let githubBranch = $state(project?.github_branch || '');
	let submitting = $state(false);
	let dialogEl: HTMLDialogElement;

	// GitHub connection state
	let ghStatus = $state<{ connected: boolean; login?: string } | null>(null);
	let ghLoading = $state(false);
	let deviceFlow = $state<{ user_code: string; verification_uri: string; device_code: string; interval: number } | null>(null);
	let deviceFlowPolling = $state(false);

	const isEditing = !!project;

	$effect(() => {
		if (dialogEl && !dialogEl.open) {
			dialogEl.showModal();
		}
	});

	// Check GitHub status on mount
	$effect(() => {
		checkGithubStatus();
	});

	async function checkGithubStatus() {
		try {
			ghStatus = await github.status();
		} catch {
			ghStatus = { connected: false };
		}
	}

	async function startDeviceFlow() {
		ghLoading = true;
		try {
			const flow = await github.startDeviceFlow();
			deviceFlow = flow;
			pollForToken(flow.device_code, flow.interval);
		} catch (err) {
			console.error('Failed to start device flow:', err);
		} finally {
			ghLoading = false;
		}
	}

	async function pollForToken(device_code: string, interval: number) {
		deviceFlowPolling = true;
		const pollInterval = Math.max(interval, 5) * 1000;

		const poll = async () => {
			if (!deviceFlowPolling) return;
			try {
				const result = await github.pollDeviceFlow(device_code);
				if (result.status === 'complete') {
					deviceFlowPolling = false;
					deviceFlow = null;
					await checkGithubStatus();
					return;
				}
				if (result.status === 'error') {
					deviceFlowPolling = false;
					deviceFlow = null;
					return;
				}
				// Still pending — poll again
				setTimeout(poll, pollInterval);
			} catch {
				deviceFlowPolling = false;
			}
		};

		setTimeout(poll, pollInterval);
	}

	function handleDialogClose() {
		deviceFlowPolling = false;
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
			await onSubmit({
				name: name.trim(),
				instructions: instructions.trim(),
				color,
				github_repo: githubRepo.trim() || undefined,
				github_branch: githubBranch.trim() || undefined,
			});
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
	class="dialog w-full sm:max-w-[500px]"
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

				<!-- GitHub Repository -->
				<div class="border-t border-border pt-4 mt-1">
					<div class="flex items-center gap-2 mb-3">
						<Github class="h-4 w-4" />
						<span class="font-medium text-sm">GitHub Repository</span>
						{#if ghStatus?.connected}
							<span class="ml-auto flex items-center gap-1 text-xs text-green-600">
								<Check class="h-3 w-3" />
								{ghStatus.login}
							</span>
						{/if}
					</div>

					{#if !ghStatus?.connected}
						<!-- Not connected — show connect button or device flow -->
						{#if deviceFlow}
							<div class="rounded-md border border-border p-3 bg-muted/50 text-sm space-y-2">
								<p>Enter this code on GitHub:</p>
								<div class="flex items-center gap-3">
									<code class="text-lg font-mono font-bold tracking-wider px-3 py-1 bg-background rounded border border-border">{deviceFlow.user_code}</code>
									<a
										href={deviceFlow.verification_uri}
										target="_blank"
										rel="noopener noreferrer"
										class="btn-outline text-xs inline-flex items-center gap-1"
									>
										Open GitHub <ExternalLink class="h-3 w-3" />
									</a>
								</div>
								<p class="text-muted-foreground flex items-center gap-1">
									<Loader2 class="h-3 w-3 animate-spin" />
									Waiting for authorization...
								</p>
							</div>
						{:else}
							<button
								type="button"
								class="btn-outline text-sm w-full"
								onclick={startDeviceFlow}
								disabled={ghLoading}
							>
								{#if ghLoading}
									<Loader2 class="h-3.5 w-3.5 animate-spin" />
								{:else}
									<Github class="h-3.5 w-3.5" />
								{/if}
								Connect GitHub Account
							</button>
						{/if}
					{:else}
						<!-- Connected — show repo fields -->
						<div class="grid gap-3">
							<div class="grid gap-1.5">
								<label for="github-repo" class="text-sm">Repository</label>
								<input
									id="github-repo"
									bind:value={githubRepo}
									placeholder="owner/repo"
									class="font-mono text-sm"
								/>
							</div>
							<div class="grid gap-1.5">
								<label for="github-branch" class="text-sm">Branch</label>
								<input
									id="github-branch"
									bind:value={githubBranch}
									placeholder="main"
									class="font-mono text-sm"
								/>
							</div>
						</div>
					{/if}
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
