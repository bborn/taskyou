<script lang="ts">
	import {
		Zap, LayoutDashboard, MessageSquare, Plus, FolderOpen, Plug, ShieldCheck,
		Settings, LogOut, Moon, Sun, Monitor, Trash2,
	} from 'lucide-svelte';
	import { onMount } from 'svelte';
	import { navState, navigate, toggleSidebar, closeMobileSidebar } from '$lib/stores/nav.svelte';
	import { chatState, fetchChats, selectChat, createNewChat, deleteChat } from '$lib/stores/chat.svelte';
	import type { User, Chat, NavView } from '$lib/types';

	interface Props {
		user: User;
		onLogout: () => void;
	}

	let { user, onLogout }: Props = $props();

	let theme = $state<'light' | 'dark' | 'system'>(
		(typeof localStorage !== 'undefined' && localStorage.getItem('theme') as 'light' | 'dark' | 'system') || 'system'
	);

	$effect(() => {
		if (typeof document === 'undefined') return;
		const root = document.documentElement;
		if (theme === 'system') {
			const systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
			root.classList.toggle('dark', systemDark);
		} else {
			root.classList.toggle('dark', theme === 'dark');
		}
	});

	function cycleTheme() {
		const themes: Array<'light' | 'dark' | 'system'> = ['light', 'dark', 'system'];
		const idx = themes.indexOf(theme);
		theme = themes[(idx + 1) % themes.length];
		if (typeof localStorage !== 'undefined') {
			localStorage.setItem('theme', theme);
		}
	}

	function getInitials(name: string) {
		return name.split(' ').map(n => n[0]).join('').toUpperCase().slice(0, 2);
	}

	const navItems: { view: NavView; label: string; icon: typeof LayoutDashboard }[] = [
		{ view: 'dashboard', label: 'Dashboard', icon: LayoutDashboard },
		{ view: 'approvals', label: 'Approvals', icon: ShieldCheck },
		{ view: 'integrations', label: 'Integrations', icon: Plug },
	];

	let collapsed = $derived(navState.sidebarCollapsed);

	async function handleNewChat() {
		const chat = await createNewChat();
		window.location.hash = `#/chat/${chat.id}`;
		closeMobileSidebar();
	}

	function handleSelectChat(chat: Chat) {
		window.location.hash = `#/chat/${chat.id}`;
		closeMobileSidebar();
	}

	async function handleDeleteChat(e: MouseEvent, chat: Chat) {
		e.stopPropagation();
		if (!confirm(`Delete "${chat.title}"?`)) return;
		const wasActive = chatState.activeChat?.id === chat.id;
		await deleteChat(chat.id);
		if (wasActive) {
			window.location.hash = '#/dashboard';
		}
	}
</script>

<!-- Mobile overlay -->
{#if navState.sidebarMobileOpen}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="fixed inset-0 bg-black/50 z-40 lg:hidden" onclick={closeMobileSidebar}></div>
{/if}

<div
	class="sidebar fixed top-0 left-0 h-full z-50 {navState.sidebarMobileOpen ? 'translate-x-0' : '-translate-x-full'} lg:translate-x-0 transition-transform"
	data-collapsed={collapsed ? '' : undefined}
>
	<nav>
		<header>
			<div class="flex items-center gap-2.5 px-1 py-0.5">
				<div class="flex items-center justify-center size-7 rounded-lg bg-sidebar-primary flex-shrink-0">
					<Zap class="size-4 text-sidebar-primary-foreground" />
				</div>
				<span class="font-semibold text-sm tracking-tight" data-sidebar-label>
					task<span class="text-primary">you</span>
				</span>
				<button
					class="ml-auto size-7 flex-shrink-0 flex items-center justify-center rounded-md text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors hidden lg:flex"
					onclick={toggleSidebar}
					title={collapsed ? 'Expand sidebar ([)' : 'Collapse sidebar ([)'}
				>
					<svg class="size-4" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/></svg>
				</button>
			</div>
		</header>

		<hr role="separator">

		<section>
			<ul>
				{#each navItems as item}
					<li>
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<!-- svelte-ignore a11y_missing_attribute -->
						<a
							href="#{item.view}"
							onclick={(e) => { e.preventDefault(); navigate(item.view); closeMobileSidebar(); }}
							aria-current={navState.view === item.view ? 'page' : undefined}
						>
							<item.icon class="size-4 flex-shrink-0" />
							<span>{item.label}</span>
						</a>
					</li>
				{/each}
			</ul>
		</section>

		<hr role="separator">

		<section>
			<div role="group">
				<div class="flex gap-1.5 px-2 py-1" data-sidebar-label>
					<button class="btn-sm flex-1 text-xs h-7" onclick={handleNewChat}>
						<Plus class="size-3.5" />
						New Chat
					</button>
				</div>
				<!-- Collapsed: icon-only new chat button -->
				<div data-sidebar-collapsed-only class="hidden px-1 py-1">
					<button
						class="w-full flex items-center justify-center rounded-md p-2 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
						onclick={handleNewChat}
						title="New Chat"
					>
						<Plus class="size-4" />
					</button>
				</div>
			</div>

			<h3 role="group" data-sidebar-label>Chats</h3>
			<ul>
				{#each chatState.chats as chat (chat.id)}
					<li>
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<a
							href="#chat-{chat.id}"
							onclick={(e) => { e.preventDefault(); handleSelectChat(chat); }}
							aria-current={chatState.activeChat?.id === chat.id ? 'page' : undefined}
							class="group"
						>
							<span class="sidebar-item-initial size-4 flex-shrink-0 rounded text-[10px] font-semibold leading-none flex items-center justify-center bg-sidebar-accent text-sidebar-accent-foreground">
								{chat.title.charAt(0).toUpperCase()}
							</span>
							<span>{chat.title}</span>
							<button
								onclick={(e) => handleDeleteChat(e, chat)}
								class="ml-auto opacity-0 group-hover:opacity-100 p-0.5 rounded hover:bg-destructive/10 transition-opacity flex-shrink-0"
							>
								<Trash2 class="size-3 text-destructive" />
							</button>
						</a>
					</li>
				{/each}
				{#if chatState.chats.length === 0}
					<li class="px-2 py-4 text-center" data-sidebar-label>
						<span class="text-xs text-sidebar-foreground/40">No chats yet</span>
					</li>
				{/if}
			</ul>
		</section>

		<hr role="separator">

		<section>
			<h3 role="group" data-sidebar-label>Projects</h3>
			<ul>
				<li>
					<!-- svelte-ignore a11y_missing_attribute -->
					<a
						href="#projects"
						onclick={(e) => { e.preventDefault(); navigate('projects'); closeMobileSidebar(); }}
						aria-current={navState.view === 'projects' ? 'page' : undefined}
					>
						<FolderOpen class="size-4 flex-shrink-0" />
						<span>Manage Projects</span>
					</a>
				</li>
			</ul>
		</section>

		<footer>
			<button
				class="flex w-full items-center gap-2 rounded-md p-2 text-sm text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
				onclick={cycleTheme}
				title="Toggle theme"
			>
				{#if theme === 'light'}
					<Sun class="size-4 flex-shrink-0" />
				{:else if theme === 'dark'}
					<Moon class="size-4 flex-shrink-0" />
				{:else}
					<Monitor class="size-4 flex-shrink-0" />
				{/if}
				<span data-sidebar-label class="capitalize">{theme}</span>
			</button>

			<button
				class="flex w-full items-center gap-2 rounded-md p-2 text-sm text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
				onclick={() => { navigate('settings'); closeMobileSidebar(); }}
				title="Settings"
			>
				<Settings class="size-4 flex-shrink-0" />
				<span data-sidebar-label>Settings</span>
			</button>

			<div class="flex items-center gap-2 rounded-md p-2" title={user.name || user.email}>
				<div class="size-7 rounded-full bg-primary/10 flex items-center justify-center text-primary text-xs font-medium flex-shrink-0">
					{getInitials(user.name || user.email)}
				</div>
				<div class="flex-1 min-w-0 overflow-hidden" data-sidebar-label>
					<p class="text-sm font-medium truncate">{user.name}</p>
					<p class="text-xs text-sidebar-foreground/50 truncate">{user.email}</p>
				</div>
				<button
					onclick={onLogout}
					class="flex-shrink-0 p-1 rounded hover:bg-destructive/10 transition-colors"
					title="Sign out"
					data-sidebar-label
				>
					<LogOut class="size-4 text-sidebar-foreground/50 hover:text-destructive" />
				</button>
			</div>
		</footer>
	</nav>
</div>
