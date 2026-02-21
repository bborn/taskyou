<script lang="ts">
	import {
		Zap, Plus, Settings, LogOut, Moon, Sun, Monitor, Trash2,
		ChevronRight, ChevronDown, MessageSquare, FileText, FolderOpen, Pencil,
	} from 'lucide-svelte';
	import { navState, navigate, toggleSidebar, closeMobileSidebar, setActiveProject } from '$lib/stores/nav.svelte';
	import { chatState, selectChat, createNewChat, deleteChat, getChatsByProject, getScratchChats } from '$lib/stores/chat.svelte';
	import { projectState, fetchProjects, toggleProjectExpanded } from '$lib/stores/projects.svelte';
	import type { User, Chat, Project } from '$lib/types';
	import ProjectDialog from './ProjectDialog.svelte';

	interface Props {
		user: User;
		onLogout: () => void;
	}

	let { user, onLogout }: Props = $props();

	let showNewProject = $state(false);
	let editingProject = $state<Project | null>(null);

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

	let collapsed = $derived(navState.sidebarCollapsed);
	let scratchChats = $derived(getScratchChats());

	function handleProjectClick(project: Project) {
		const isExpanded = projectState.expandedProjects.has(project.id);
		const isActive = navState.activeProjectId === project.id;

		if (!isExpanded) {
			// Collapsed: expand + set active + go to dashboard
			toggleProjectExpanded(project.id);
			setActiveProject(project.id);
		} else if (isActive) {
			// Expanded and already active: collapse
			toggleProjectExpanded(project.id);
		} else {
			// Expanded but different project: set active
			setActiveProject(project.id);
		}
		closeMobileSidebar();
	}

	async function handleNewChatInProject(e: MouseEvent, projectId: string) {
		e.stopPropagation();
		const chat = await createNewChat(projectId);
		setActiveProject(projectId);
		window.location.hash = `#/chat/${chat.id}`;
		closeMobileSidebar();
	}

	function handleSelectChat(chat: Chat) {
		// Set parent project as active if chat has one
		if (chat.project_id) {
			setActiveProject(chat.project_id);
			// Make sure the project is expanded
			if (!projectState.expandedProjects.has(chat.project_id)) {
				toggleProjectExpanded(chat.project_id);
			}
		} else {
			setActiveProject(null);
		}
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

	function handleScratchPad() {
		setActiveProject(null);
		navigate('dashboard');
		closeMobileSidebar();
	}

	async function handleNewScratchChat() {
		const chat = await createNewChat();
		setActiveProject(null);
		window.location.hash = `#/chat/${chat.id}`;
		closeMobileSidebar();
	}

	async function handleProjectCreated(data: { name: string; instructions?: string; color?: string; github_repo?: string; github_branch?: string }) {
		const { createProject } = await import('$lib/stores/projects.svelte');
		const project = await createProject(data);
		setActiveProject(project.id);
		toggleProjectExpanded(project.id);
	}

	async function handleProjectUpdated(data: { name?: string; instructions?: string; color?: string; github_repo?: string; github_branch?: string }) {
		if (!editingProject) return;
		const { updateProject } = await import('$lib/stores/projects.svelte');
		await updateProject(editingProject.id, data);
	}

	async function handleProjectDeleted() {
		if (!editingProject) return;
		const { deleteProject } = await import('$lib/stores/projects.svelte');
		await deleteProject(editingProject.id);
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

		<!-- New Project button -->
		<section>
			<div class="px-2 py-1" data-sidebar-label>
				<button class="btn-sm w-full text-xs h-7" onclick={() => (showNewProject = true)}>
					<Plus class="size-3.5" />
					New Project
				</button>
			</div>
			<div data-sidebar-collapsed-only class="hidden px-1 py-1">
				<button
					class="w-full flex items-center justify-center rounded-md p-2 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
					onclick={() => (showNewProject = true)}
					title="New Project"
				>
					<Plus class="size-4" />
				</button>
			</div>
		</section>

		<!-- Projects tree -->
		<section class="flex-1 overflow-y-auto">
			<h3 role="group" data-sidebar-label>Projects</h3>
			{#if projectState.projects.length === 0 && !projectState.loading}
				<div class="px-3 py-4 text-center" data-sidebar-label>
					<p class="text-xs text-sidebar-foreground/40">Create a project to connect a GitHub repo and start working.</p>
				</div>
			{/if}
			<ul role="tree">
				{#each projectState.projects as project (project.id)}
					{@const isExpanded = projectState.expandedProjects.has(project.id)}
					{@const isActive = navState.activeProjectId === project.id}
					{@const projectChats = getChatsByProject(project.id)}
					<li role="treeitem" aria-expanded={isExpanded}>
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<a
							href="#project-{project.id}"
							onclick={(e) => { e.preventDefault(); handleProjectClick(project); }}
							aria-current={isActive && navState.view === 'dashboard' ? 'page' : undefined}
							class="group"
						>
							{#if isExpanded}
								<ChevronDown class="size-3 flex-shrink-0 text-sidebar-foreground/40" />
							{:else}
								<ChevronRight class="size-3 flex-shrink-0 text-sidebar-foreground/40" />
							{/if}
							<span class="size-2 rounded-full flex-shrink-0" style:background-color={project.color || 'var(--sidebar-primary)'}></span>
							<span class="truncate">{project.name}</span>
							<button
								onclick={(e) => { e.stopPropagation(); e.preventDefault(); editingProject = project; }}
								class="ml-auto opacity-0 group-hover:opacity-100 p-0.5 rounded hover:bg-sidebar-accent transition-opacity flex-shrink-0"
								title="Edit project"
							>
								<Pencil class="size-3 text-sidebar-foreground/50" />
							</button>
						</a>

						<!-- Nested chats -->
						{#if isExpanded}
							<ul role="group" class="ml-3 border-l border-sidebar-border">
								{#each projectChats as chat (chat.id)}
									<li role="treeitem">
										<!-- svelte-ignore a11y_click_events_have_key_events -->
										<!-- svelte-ignore a11y_no_static_element_interactions -->
										<a
											href="#chat-{chat.id}"
											onclick={(e) => { e.preventDefault(); handleSelectChat(chat); }}
											aria-current={chatState.activeChat?.id === chat.id ? 'page' : undefined}
											class="group pl-2"
										>
											<MessageSquare class="size-3.5 flex-shrink-0 text-sidebar-foreground/50" />
											<span class="truncate">{chat.title}</span>
											<button
												onclick={(e) => handleDeleteChat(e, chat)}
												class="ml-auto opacity-0 group-hover:opacity-100 p-0.5 rounded hover:bg-destructive/10 transition-opacity flex-shrink-0"
											>
												<Trash2 class="size-3 text-destructive" />
											</button>
										</a>
									</li>
								{/each}
								<li>
									<button
										onclick={(e) => handleNewChatInProject(e, project.id)}
										class="flex items-center gap-1.5 w-full pl-2 px-2 py-1 text-xs text-sidebar-foreground/50 hover:text-sidebar-foreground hover:bg-sidebar-accent rounded-md transition-colors"
									>
										<Plus class="size-3" />
										<span>New Chat</span>
									</button>
								</li>
							</ul>
						{/if}
					</li>
				{/each}
			</ul>
		</section>

		<hr role="separator">

		<!-- Scratch Pad -->
		<section>
			<ul>
				<li>
					<!-- svelte-ignore a11y_missing_attribute -->
					<a
						href="#scratch"
						onclick={(e) => { e.preventDefault(); handleScratchPad(); }}
						aria-current={!navState.activeProjectId && navState.view === 'dashboard' ? 'page' : undefined}
					>
						<FileText class="size-4 flex-shrink-0" />
						<span>Scratch Pad</span>
					</a>
				</li>
			</ul>
			{#if scratchChats.length > 0}
				<ul class="ml-3">
					{#each scratchChats as chat (chat.id)}
						<li>
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<a
								href="#chat-{chat.id}"
								onclick={(e) => { e.preventDefault(); handleSelectChat(chat); }}
								aria-current={chatState.activeChat?.id === chat.id ? 'page' : undefined}
								class="group"
							>
								<MessageSquare class="size-3.5 flex-shrink-0 text-sidebar-foreground/50" />
								<span class="truncate">{chat.title}</span>
								<button
									onclick={(e) => handleDeleteChat(e, chat)}
									class="ml-auto opacity-0 group-hover:opacity-100 p-0.5 rounded hover:bg-destructive/10 transition-opacity flex-shrink-0"
								>
									<Trash2 class="size-3 text-destructive" />
								</button>
							</a>
						</li>
					{/each}
				</ul>
			{/if}
			<div class="px-2 py-1" data-sidebar-label>
				<button
					class="flex items-center gap-1.5 w-full px-2 py-1 text-xs text-sidebar-foreground/50 hover:text-sidebar-foreground hover:bg-sidebar-accent rounded-md transition-colors"
					onclick={handleNewScratchChat}
				>
					<Plus class="size-3" />
					<span>New Chat</span>
				</button>
			</div>
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

{#if showNewProject}
	<ProjectDialog
		onSubmit={handleProjectCreated}
		onClose={() => (showNewProject = false)}
	/>
{/if}

{#if editingProject}
	<ProjectDialog
		project={editingProject}
		onSubmit={handleProjectUpdated}
		onDelete={handleProjectDeleted}
		onClose={() => (editingProject = null)}
	/>
{/if}
