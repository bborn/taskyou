<script lang="ts">
	import { untrack } from 'svelte';
	import { authState, fetchUser, logout } from '$lib/stores/auth.svelte';
	import { fetchTasks } from '$lib/stores/tasks.svelte';
	import { chatState, fetchChats, selectChat, loadRestoredMessages } from '$lib/stores/chat.svelte';
	import { navState, navigate, toggleMobileSidebar, setActiveProject } from '$lib/stores/nav.svelte';
	import { handleAgentMessage, resetChatState, setOnMessagesRestored, setOnTasksUpdated } from '$lib/stores/agent.svelte';
	import { fetchProjects } from '$lib/stores/projects.svelte';

	// Wire up callbacks (must be in +page.svelte to survive tree-shaking)
	setOnMessagesRestored(loadRestoredMessages);
	setOnTasksUpdated(() => fetchTasks());
	import LoginPage from '$lib/components/LoginPage.svelte';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import Dashboard from '$lib/components/Dashboard.svelte';
	import SettingsPage from '$lib/components/SettingsPage.svelte';
	import ApprovalsPage from '$lib/components/ApprovalsPage.svelte';
	import IntegrationsPage from '$lib/components/IntegrationsPage.svelte';
	import { Menu } from 'lucide-svelte';

	let initialized = $state(false);
	let currentChatId = $state<string | null>(null);

	// Agent WebSocket connection — stored on globalThis to survive tree-shaking
	// Each chat gets its own DO instance keyed by {userId}:{chatId}
	function connectAgentWebSocket(userId: string, chatId: string) {
		disconnectAgentWebSocket();
		resetChatState();

		const G = globalThis as any;
		if (!G.__agentWs) {
			G.__agentWs = { ws: null, timer: null, connected: false, chatId: null };
		}
		const st = G.__agentWs;
		st.chatId = chatId;

		const instanceName = `${userId}:${chatId}`;
		const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
		const url = `${proto}//${window.location.host}/agents/taskyou-agent/${encodeURIComponent(instanceName)}`;
		console.log('[page] Connecting agent WS to', url);

		const ws = new WebSocket(url);
		st.ws = ws;

		ws.addEventListener('open', () => {
			console.log('[page] Agent WS connected for chat', chatId);
			st.connected = true;
			handleAgentMessage({ type: '_connected' });
		});
		ws.addEventListener('message', (ev: MessageEvent) => {
			// Ignore messages from stale connections
			if (st.chatId !== chatId) return;
			try { handleAgentMessage(JSON.parse(ev.data)); } catch {}
		});
		ws.addEventListener('close', () => {
			// Only reconnect if this is still the active chat
			if (st.chatId !== chatId) return;
			console.log('[page] Agent WS closed, reconnecting...');
			st.connected = false;
			st.ws = null;
			handleAgentMessage({ type: '_disconnected' });
			st.timer = setTimeout(() => connectAgentWebSocket(userId, chatId), 3000);
		});
		ws.addEventListener('error', () => {});
	}

	function disconnectAgentWebSocket() {
		const G = globalThis as any;
		const st = G.__agentWs;
		if (!st) return;
		if (st.timer) { clearTimeout(st.timer); st.timer = null; }
		st.chatId = null;
		if (st.ws) { st.ws.close(); st.ws = null; }
		st.connected = false;
	}

	// Parse hash route — returns chatId if #/chat/{id}, null otherwise
	function parseChatIdFromHash(): string | null {
		const hash = window.location.hash;
		const match = hash.match(/^#\/chat\/(.+)$/);
		return match ? match[1] : null;
	}

	// Handle hash route changes — connect to the right chat's agent
	function handleRoute() {
		const chatId = parseChatIdFromHash();

		if (chatId) {
			// Find the chat in our list
			const chat = chatState.chats.find(c => c.id === chatId);
			if (chat) {
				if (currentChatId !== chatId) {
					currentChatId = chatId;
					selectChat(chat);
					// Set active project based on chat's project
					if (chat.project_id) {
						setActiveProject(chat.project_id);
					}
					if (authState.user) {
						connectAgentWebSocket(authState.user.id, chatId);
					}
				}
				navigate('dashboard');
			} else {
				// Chat not found — go to dashboard
				window.location.hash = '#/dashboard';
			}
		} else {
			// No chat route — disconnect and show dashboard
			if (currentChatId) {
				currentChatId = null;
				chatState.activeChat = null;
				chatState.agentMessages = [];
				disconnectAgentWebSocket();
				resetChatState();
			}
			navigate('dashboard');
		}
	}

	$effect(() => {
		if (initialized) return;
		initialized = true;

		untrack(() => {
			fetchUser().then(async () => {
				if (authState.user) {
					fetchTasks();
					fetchProjects();
					await fetchChats();
					// Route to chat if hash is present, otherwise just show dashboard
					handleRoute();
					// Listen for hash changes (back/forward, sidebar clicks)
					window.addEventListener('hashchange', handleRoute);
				}
			});
		});

		return () => {
			window.removeEventListener('hashchange', handleRoute);
			disconnectAgentWebSocket();
		};
	});

	function handleLogout() {
		disconnectAgentWebSocket();
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

	<!-- Main content area -->
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
			{/if}
		</main>
	</div>
{/if}
