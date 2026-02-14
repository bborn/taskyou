<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { user, loading, fetchUser, logout } from '$lib/stores/auth';
	import { fetchTasks, startPolling, stopPolling } from '$lib/stores/tasks';
	import LoginPage from '$lib/components/LoginPage.svelte';
	import Dashboard from '$lib/components/Dashboard.svelte';
	import SettingsPage from '$lib/components/SettingsPage.svelte';

	let view = $state<'dashboard' | 'settings'>('dashboard');

	onMount(async () => {
		await fetchUser();
		if ($user) {
			await fetchTasks();
			startPolling();
		}
	});

	onDestroy(() => {
		stopPolling();
	});

	function handleLogout() {
		stopPolling();
		logout();
	}
</script>

{#if $loading}
	<div class="min-h-screen flex items-center justify-center bg-background">
		<div class="flex flex-col items-center gap-4">
			<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			<p class="text-muted-foreground">Loading...</p>
		</div>
	</div>
{:else if !$user}
	<LoginPage />
{:else if view === 'settings'}
	<SettingsPage onBack={() => (view = 'dashboard')} />
{:else}
	<Dashboard
		user={$user}
		onLogout={handleLogout}
		onSettings={() => (view = 'settings')}
	/>
{/if}
