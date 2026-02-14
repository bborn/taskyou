<script lang="ts">
	import { onMount } from 'svelte';
	import { Plug, Github, Mail, MessageSquare, BarChart3, CheckCircle, XCircle, AlertCircle } from 'lucide-svelte';
	import { integrations as integrationsApi } from '$lib/api/client';
	import type { Integration } from '$lib/types';

	let integrations = $state<Integration[]>([]);
	let loading = $state(true);

	onMount(async () => {
		try {
			integrations = await integrationsApi.list();
		} catch (e) {
			console.error(e);
		} finally {
			loading = false;
		}
	});

	const providers = [
		{
			id: 'github',
			name: 'GitHub',
			description: 'Connect repositories, create PRs, manage issues',
			icon: Github,
			color: 'bg-gray-900 dark:bg-white dark:text-gray-900',
		},
		{
			id: 'gmail',
			name: 'Gmail',
			description: 'Send and receive emails, process inbound messages',
			icon: Mail,
			color: 'bg-red-500',
		},
		{
			id: 'slack',
			name: 'Slack',
			description: 'Send notifications, receive commands',
			icon: MessageSquare,
			color: 'bg-purple-600',
		},
		{
			id: 'linear',
			name: 'Linear',
			description: 'Sync issues, track progress, update status',
			icon: BarChart3,
			color: 'bg-blue-600',
		},
	];

	function getStatus(providerId: string): string {
		const integration = integrations.find(i => i.provider === providerId);
		return integration?.status || 'disconnected';
	}
</script>

<div class="h-full overflow-y-auto p-6">
	<div class="max-w-4xl mx-auto">
		<div class="flex items-center gap-3 mb-6">
			<Plug class="h-6 w-6 text-primary" />
			<h1 class="text-2xl font-bold">Integrations</h1>
		</div>

		<p class="text-muted-foreground mb-8">
			Connect external services to extend Pilot's capabilities.
		</p>

		{#if loading}
			<div class="flex items-center justify-center py-20">
				<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			</div>
		{:else}
			<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
				{#each providers as provider}
					{@const status = getStatus(provider.id)}
					<div class="card p-5 hover:shadow-md transition-shadow">
						<div class="flex items-start gap-4">
							<div class="p-2.5 rounded-lg text-white {provider.color} shrink-0">
								<provider.icon class="h-5 w-5" />
							</div>
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2 mb-1">
									<h3 class="font-semibold">{provider.name}</h3>
									{#if status === 'connected'}
										<span class="badge-secondary text-green-500">
											<CheckCircle class="h-3 w-3" />
											Connected
										</span>
									{:else if status === 'error'}
										<span class="badge-destructive">
											<AlertCircle class="h-3 w-3" />
											Error
										</span>
									{/if}
								</div>
								<p class="text-sm text-muted-foreground mb-4">{provider.description}</p>
								{#if status === 'connected'}
									<button class="btn-sm-outline">Disconnect</button>
								{:else}
									<button class="btn-sm">Connect</button>
								{/if}
							</div>
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
