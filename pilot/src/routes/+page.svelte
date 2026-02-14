<script lang="ts">
	import { untrack } from 'svelte';
	import { authState, fetchUser, logout } from '$lib/stores/auth.svelte';
	import { fetchTasks, startPolling, stopPolling } from '$lib/stores/tasks.svelte';
	import { fetchChats } from '$lib/stores/chat.svelte';
	import { navState, toggleMobileSidebar } from '$lib/stores/nav.svelte';
	import LoginPage from '$lib/components/LoginPage.svelte';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import Dashboard from '$lib/components/Dashboard.svelte';
	import SettingsPage from '$lib/components/SettingsPage.svelte';
	import ApprovalsPage from '$lib/components/ApprovalsPage.svelte';
	import IntegrationsPage from '$lib/components/IntegrationsPage.svelte';
	import SandboxesPage from '$lib/components/SandboxesPage.svelte';
	import { Menu } from 'lucide-svelte';

	let initialized = $state(false);

	$effect(() => {
		if (initialized) return;
		initialized = true;

		untrack(() => {
			fetchUser().then(() => {
				if (authState.user) {
					fetchTasks();
					fetchChats();
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
{:else}
	<!-- Sidebar -->
	<Sidebar user={authState.user} onLogout={handleLogout} />

	<!-- Main content area (adjacent sibling of .sidebar, gets ml via --sidebar-width) -->
	<div class="h-screen flex flex-col overflow-hidden lg:ml-[var(--sidebar-width)] transition-[margin] duration-200">
		<!-- Mobile header -->
		<div class="lg:hidden flex items-center gap-3 h-12 px-4 border-b border-border bg-card shrink-0">
			<button onclick={toggleMobileSidebar} class="p-1.5 rounded-lg hover:bg-muted">
				<Menu class="h-5 w-5" />
			</button>
			<span class="font-semibold text-sm">TaskYou</span>
		</div>

		<!-- Page content -->
		<main class="flex-1 min-h-0">
			{#if navState.view === 'dashboard'}
				<Dashboard user={authState.user} />
			{:else if navState.view === 'settings'}
				<SettingsPage onBack={() => (navState.view = 'dashboard')} />
			{:else if navState.view === 'approvals'}
				<ApprovalsPage />
			{:else if navState.view === 'integrations'}
				<IntegrationsPage />
			{:else if navState.view === 'sandboxes'}
				<SandboxesPage />
			{/if}
		</main>
	</div>
{/if}
