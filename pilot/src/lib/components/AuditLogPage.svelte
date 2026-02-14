<script lang="ts">
	import { onMount } from 'svelte';
	import { ScrollText, Shield, AlertTriangle, DollarSign, Clock } from 'lucide-svelte';
	import { agentActions } from '$lib/api/client';
	import type { AgentAction } from '$lib/types';

	let actions = $state<AgentAction[]>([]);
	let loading = $state(true);

	onMount(async () => {
		try {
			actions = await agentActions.list({ limit: 50 });
		} catch (e) {
			console.error(e);
		} finally {
			loading = false;
		}
	});

	const riskColors: Record<string, string> = {
		low: 'text-green-500 bg-green-500/10',
		medium: 'text-yellow-500 bg-yellow-500/10',
		high: 'text-red-500 bg-red-500/10',
	};

	const statusColors: Record<string, string> = {
		completed: 'text-green-500',
		pending_approval: 'text-yellow-500',
		rejected: 'text-red-500',
		failed: 'text-red-500',
	};
</script>

<div class="h-full overflow-y-auto p-6">
	<div class="max-w-5xl mx-auto">
		<div class="flex items-center gap-3 mb-6">
			<ScrollText class="h-6 w-6 text-primary" />
			<h1 class="text-2xl font-bold">Audit Log</h1>
		</div>

		{#if loading}
			<div class="flex items-center justify-center py-20">
				<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			</div>
		{:else if actions.length === 0}
			<div class="flex flex-col items-center justify-center py-20 text-center">
				<div class="p-4 rounded-full bg-muted mb-4">
					<ScrollText class="h-8 w-8 text-muted-foreground" />
				</div>
				<h3 class="font-semibold mb-1">No Actions Yet</h3>
				<p class="text-sm text-muted-foreground">Agent actions will appear here as they execute</p>
			</div>
		{:else}
			<div class="table">
				<table>
					<thead>
						<tr>
							<th>Action</th>
							<th>Risk</th>
							<th>Cost</th>
							<th>Status</th>
							<th>Time</th>
						</tr>
					</thead>
					<tbody>
						{#each actions as action}
							<tr>
								<td>
									<div class="font-medium">{action.action_type}</div>
									<div class="text-xs text-muted-foreground mt-0.5 line-clamp-1">{action.description}</div>
								</td>
								<td>
									{#if action.risk_level === 'high'}
										<span class="badge-destructive">
											<AlertTriangle class="h-3 w-3" />
											{action.risk_level}
										</span>
									{:else if action.risk_level === 'medium'}
										<span class="badge" style="background-color: oklch(0.7 0.17 85); color: white;">
											<Shield class="h-3 w-3" />
											{action.risk_level}
										</span>
									{:else}
										<span class="badge-secondary">
											<Shield class="h-3 w-3" />
											{action.risk_level}
										</span>
									{/if}
								</td>
								<td>
									{#if action.cost_cents > 0}
										<span class="flex items-center gap-1 text-muted-foreground">
											<DollarSign class="h-3 w-3" />
											{(action.cost_cents / 100).toFixed(2)}
										</span>
									{:else}
										<span class="text-muted-foreground">-</span>
									{/if}
								</td>
								<td>
									<span class="{statusColors[action.status] || 'text-muted-foreground'} text-xs font-medium capitalize">
										{action.status.replace('_', ' ')}
									</span>
								</td>
								<td class="text-xs text-muted-foreground">
									{new Date(action.created_at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>
</div>
