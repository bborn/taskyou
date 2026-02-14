<script lang="ts">
	import { Search, Settings, LogOut, Moon, Sun, Monitor, Zap, ChevronDown } from 'lucide-svelte';
	import type { User } from '$lib/types';

	interface Props {
		user: User;
		onLogout: () => void;
		onSettings: () => void;
		onCommandPalette: () => void;
	}

	let { user, onLogout, onSettings, onCommandPalette }: Props = $props();

	let showUserMenu = $state(false);
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
		return name.split(' ').map((n) => n[0]).join('').toUpperCase().slice(0, 2);
	}
</script>

<header class="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur-lg">
	<div class="container mx-auto px-4">
		<div class="flex h-16 items-center justify-between">
			<!-- Logo -->
			<div class="flex items-center gap-3">
				<div class="flex items-center gap-2">
					<div class="h-8 w-8 rounded-lg bg-gradient-to-br from-primary to-purple-600 flex items-center justify-center">
						<Zap class="h-5 w-5 text-white" />
					</div>
					<span class="font-bold text-xl">
						task<span class="text-primary">you</span>
					</span>
				</div>
			</div>

			<!-- Center - Search Trigger -->
			<button
				onclick={onCommandPalette}
				class="hidden md:flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border bg-muted/50 hover:bg-muted text-sm text-muted-foreground transition-colors min-w-[280px]"
				title="Search tasks (⌘K)"
			>
				<Search class="h-4 w-4" />
				<span class="flex-1 text-left">Search tasks...</span>
				<kbd class="kbd">&#8984;K</kbd>
			</button>

			<!-- Right side -->
			<div class="flex items-center gap-2">
				<!-- Mobile search -->
				<button class="btn-sm-icon-ghost md:hidden" onclick={onCommandPalette} title="Search (⌘K)">
					<Search class="h-5 w-5" />
				</button>

				<!-- Theme toggle -->
				<button
					class="btn-sm-icon-ghost"
					onclick={cycleTheme}
					title="Theme: {theme}"
				>
					{#if theme === 'light'}
						<Sun class="h-5 w-5" />
					{:else if theme === 'dark'}
						<Moon class="h-5 w-5" />
					{:else}
						<Monitor class="h-5 w-5" />
					{/if}
				</button>

				<!-- Settings -->
				<button class="btn-sm-icon-ghost" onclick={onSettings}>
					<Settings class="h-5 w-5" />
				</button>

				<!-- User menu -->
				<div class="relative">
					<button
						onclick={() => (showUserMenu = !showUserMenu)}
						class="flex items-center gap-2 p-1.5 rounded-lg hover:bg-muted transition-colors"
					>
						<div class="h-8 w-8 rounded-full bg-primary/10 flex items-center justify-center text-primary text-xs font-medium">
							{getInitials(user.name || user.email)}
						</div>
						<ChevronDown class="h-4 w-4 text-muted-foreground hidden sm:block" />
					</button>

					{#if showUserMenu}
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div class="fixed inset-0 z-40" onclick={() => (showUserMenu = false)}></div>
						<div class="absolute right-0 top-full mt-2 w-56 rounded-lg border border-border bg-card shadow-lg z-50 py-1">
							<div class="px-3 py-2 border-b border-border">
								<p class="font-medium text-sm truncate">{user.name}</p>
								<p class="text-xs text-muted-foreground truncate">{user.email}</p>
							</div>
							<button
								onclick={() => { showUserMenu = false; onSettings(); }}
								class="w-full flex items-center gap-2 px-3 py-2 text-sm hover:bg-muted transition-colors"
							>
								<Settings class="h-4 w-4" />
								Settings
							</button>
							<button
								onclick={() => { showUserMenu = false; onLogout(); }}
								class="w-full flex items-center gap-2 px-3 py-2 text-sm text-red-500 hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors"
							>
								<LogOut class="h-4 w-4" />
								Sign out
							</button>
						</div>
					{/if}
				</div>
			</div>
		</div>
	</div>
</header>
