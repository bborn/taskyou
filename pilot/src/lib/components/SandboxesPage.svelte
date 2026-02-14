<script lang="ts">
	import { onMount } from 'svelte';
	import { Box, Plus, Play, Square, Trash2, Terminal, RefreshCw } from 'lucide-svelte';
	import { sandboxes as sandboxesApi } from '$lib/api/client';
	import type { Sandbox } from '$lib/types';

	let sandboxes = $state<Sandbox[]>([]);
	let loading = $state(true);

	onMount(async () => {
		try {
			sandboxes = await sandboxesApi.list();
		} catch (e) {
			console.error(e);
		} finally {
			loading = false;
		}
	});

	const statusColors: Record<string, { dot: string; text: string; bg: string }> = {
		running: { dot: 'bg-green-500', text: 'text-green-500', bg: 'bg-green-500/10' },
		pending: { dot: 'bg-yellow-500', text: 'text-yellow-500', bg: 'bg-yellow-500/10' },
		provisioning: { dot: 'bg-blue-500 animate-pulse', text: 'text-blue-500', bg: 'bg-blue-500/10' },
		stopped: { dot: 'bg-gray-400', text: 'text-gray-400', bg: 'bg-gray-400/10' },
		error: { dot: 'bg-red-500', text: 'text-red-500', bg: 'bg-red-500/10' },
	};

	async function handleCreate() {
		try {
			const sandbox = await sandboxesApi.create({ name: `Sandbox ${sandboxes.length + 1}` });
			sandboxes = [sandbox, ...sandboxes];
		} catch (e) {
			console.error(e);
		}
	}

	async function handleStart(id: string) {
		try {
			const updated = await sandboxesApi.start(id);
			sandboxes = sandboxes.map(s => s.id === id ? updated : s);
		} catch (e) {
			console.error(e);
		}
	}

	async function handleStop(id: string) {
		try {
			const updated = await sandboxesApi.stop(id);
			sandboxes = sandboxes.map(s => s.id === id ? updated : s);
		} catch (e) {
			console.error(e);
		}
	}

	async function handleDelete(id: string) {
		try {
			await sandboxesApi.delete(id);
			sandboxes = sandboxes.filter(s => s.id !== id);
		} catch (e) {
			console.error(e);
		}
	}
</script>

<div class="h-full overflow-y-auto p-6">
	<div class="max-w-4xl mx-auto">
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<Box class="h-6 w-6 text-primary" />
				<h1 class="text-2xl font-bold">Sandboxes</h1>
			</div>
			<button class="btn" onclick={handleCreate}>
				<Plus class="h-4 w-4" />
				New Sandbox
			</button>
		</div>

		<p class="text-muted-foreground mb-8">
			Isolated execution environments for running tasks. Powered by Cloudflare Containers.
		</p>

		{#if loading}
			<div class="flex items-center justify-center py-20">
				<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			</div>
		{:else if sandboxes.length === 0}
			<div class="flex flex-col items-center justify-center py-20 text-center">
				<div class="p-4 rounded-full bg-muted mb-4">
					<Box class="h-8 w-8 text-muted-foreground" />
				</div>
				<h3 class="font-semibold mb-1">No Sandboxes</h3>
				<p class="text-sm text-muted-foreground mb-4">Create a sandbox to start executing tasks</p>
				<button class="btn" onclick={handleCreate}>
					<Plus class="h-4 w-4" />
					Create Sandbox
				</button>
			</div>
		{:else}
			<div class="space-y-4">
				{#each sandboxes as sandbox (sandbox.id)}
					{@const colors = statusColors[sandbox.status] || statusColors.stopped}
					<div class="card p-5">
						<div class="flex items-center justify-between">
							<div class="flex items-center gap-4">
								<div class="p-2.5 rounded-lg bg-muted">
									<Box class="h-5 w-5 text-foreground" />
								</div>
								<div>
									<div class="flex items-center gap-2">
										<h3 class="font-semibold">{sandbox.name}</h3>
										<span class="badge-outline {colors.text}">
											<span class="h-1.5 w-1.5 rounded-full {colors.dot}"></span>
											{sandbox.status}
										</span>
									</div>
									<p class="text-xs text-muted-foreground mt-0.5">
										{sandbox.provider} &bull; Created {new Date(sandbox.created_at).toLocaleDateString()}
									</p>
								</div>
							</div>
							<div class="flex items-center gap-2">
								{#if sandbox.status === 'stopped' || sandbox.status === 'pending'}
									<button class="btn-sm bg-green-600 hover:bg-green-700 text-white border-green-600" onclick={() => handleStart(sandbox.id)}>
										<Play class="h-3.5 w-3.5" />
										Start
									</button>
								{:else if sandbox.status === 'running'}
									<button class="btn-sm-secondary" onclick={() => handleStop(sandbox.id)}>
										<Square class="h-3.5 w-3.5" />
										Stop
									</button>
								{/if}
								<button
									class="btn-sm-icon-ghost"
									onclick={() => handleDelete(sandbox.id)}
									title="Delete sandbox"
								>
									<Trash2 class="h-4 w-4 text-muted-foreground hover:text-destructive" />
								</button>
							</div>
						</div>

						<!-- Terminal area (collapsible) -->
						{#if sandbox.status === 'running'}
							<div class="mt-4 rounded-lg bg-gray-950 p-3 font-mono text-xs text-gray-300 h-32 overflow-y-auto scrollbar-thin">
								<div class="text-green-400">$ sandbox ready</div>
								<div class="text-muted-foreground">Waiting for tasks...</div>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
