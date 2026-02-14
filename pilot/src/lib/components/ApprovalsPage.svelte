<script lang="ts">
	import { onMount } from 'svelte';
	import { ShieldCheck, CheckCircle, XCircle, Clock, AlertTriangle } from 'lucide-svelte';
	import { taskState } from '$lib/stores/tasks.svelte';
	import type { Task } from '$lib/types';

	let pendingTasks = $derived(
		taskState.tasks.filter(t => t.approval_status === 'pending_review')
	);

	let blockedTasks = $derived(
		taskState.tasks.filter(t => t.status === 'blocked')
	);
</script>

<div class="h-full overflow-y-auto p-6">
	<div class="max-w-4xl mx-auto">
		<div class="flex items-center gap-3 mb-6">
			<ShieldCheck class="h-6 w-6 text-primary" />
			<h1 class="text-2xl font-bold">Approvals</h1>
		</div>

		{#if pendingTasks.length === 0 && blockedTasks.length === 0}
			<div class="flex flex-col items-center justify-center py-20 text-center">
				<div class="p-4 rounded-full bg-green-500/10 mb-4">
					<CheckCircle class="h-8 w-8 text-green-500" />
				</div>
				<h3 class="font-semibold mb-1">All Clear</h3>
				<p class="text-sm text-muted-foreground">No tasks pending approval</p>
			</div>
		{:else}
			<!-- Pending Approvals -->
			{#if pendingTasks.length > 0}
				<div class="mb-8">
					<h2 class="text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-3">Pending Review</h2>
					<div class="space-y-3">
						{#each pendingTasks as task}
							<div class="card p-4">
								<div class="flex items-start justify-between">
									<div>
										<h3 class="font-medium">{task.title}</h3>
										{#if task.body}
											<p class="text-sm text-muted-foreground mt-1 line-clamp-2">{task.body}</p>
										{/if}
										<div class="flex items-center gap-2 mt-2">
											<span class="badge-secondary">{task.type}</span>
											<span class="text-xs text-muted-foreground">{task.project}</span>
										</div>
									</div>
									<div class="flex gap-2 shrink-0">
										<button class="btn-sm bg-green-600 hover:bg-green-700 text-white border-green-600">
											<CheckCircle class="h-3.5 w-3.5" />
											Approve
										</button>
										<button class="btn-sm-destructive">
											<XCircle class="h-3.5 w-3.5" />
											Reject
										</button>
									</div>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			<!-- Blocked tasks needing attention -->
			{#if blockedTasks.length > 0}
				<div>
					<h2 class="text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-3">Needs Attention</h2>
					<div class="space-y-3">
						{#each blockedTasks as task}
							<div class="card p-4">
								<div class="flex items-center gap-3">
									<AlertTriangle class="h-5 w-5 text-orange-500 shrink-0" />
									<div class="flex-1 min-w-0">
										<h3 class="font-medium">{task.title}</h3>
										<p class="text-xs text-muted-foreground mt-0.5">Blocked since {new Date(task.updated_at).toLocaleDateString()}</p>
									</div>
									<span class="badge-secondary">{task.type}</span>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}
		{/if}
	</div>
</div>
