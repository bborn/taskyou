<script lang="ts">
	import { untrack } from 'svelte';
	import { authState, fetchUser, logout } from '$lib/stores/auth.svelte';
	import { fetchTasks, startPolling, stopPolling } from '$lib/stores/tasks.svelte';
	import LoginPage from '$lib/components/LoginPage.svelte';
	import Dashboard from '$lib/components/Dashboard.svelte';
	import SettingsPage from '$lib/components/SettingsPage.svelte';

	let view = $state<'dashboard' | 'settings'>('dashboard');
	let initialized = $state(false);

	$effect(() => {
		if (initialized) return;
		initialized = true;

		untrack(() => {
			fetchUser().then(() => {
				if (authState.user) {
					fetchTasks();
					startPolling();
				}
			});
		});

		return () => {
			stopPolling();
		};
	});

	function handleLogout() {
		stopPolling();
		logout();
	}
</script>

{#if authState.loading}
	<div class="min-h-screen flex items-center justify-center bg-background">
		<div class="flex flex-col items-center gap-4">
			<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			<p class="text-muted-foreground">Loading...</p>
		</div>
	</div>
{:else if !authState.user}
	<LoginPage />
{:else if view === 'settings'}
	<SettingsPage onBack={() => (view = 'dashboard')} />
{:else}
	<Dashboard
		user={authState.user}
		onLogout={handleLogout}
		onSettings={() => (view = 'settings')}
	/>
{/if}
