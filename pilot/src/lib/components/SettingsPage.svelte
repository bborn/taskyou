<script lang="ts">
	import { onMount } from 'svelte';
	import { ArrowLeft, Plus, Folder, Palette, Settings2, Download, Check } from 'lucide-svelte';
	import { settings as settingsApi, projects as projectsApi } from '$lib/api/client';
	import type { Project } from '$lib/types';
	import ProjectDialog from './ProjectDialog.svelte';

	interface Props {
		onBack: () => void;
	}

	let { onBack }: Props = $props();

	let settings = $state<Record<string, string>>({});
	let projects = $state<Project[]>([]);
	let loading = $state(true);
	let saving = $state(false);
	let saved = $state(false);
	let editingProject = $state<Project | null>(null);
	let showNewProject = $state(false);

	onMount(async () => {
		try {
			const [s, p] = await Promise.all([settingsApi.get(), projectsApi.list()]);
			settings = s;
			projects = p;
		} catch (e) {
			console.error(e);
		} finally {
			loading = false;
		}
	});

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') onBack();
	}

	async function handleSave() {
		saving = true;
		try {
			await settingsApi.update(settings);
			saved = true;
			setTimeout(() => (saved = false), 2000);
		} catch (e) { console.error(e); }
		finally { saving = false; }
	}

	async function handleCreateProject(data: { name: string; path: string; aliases?: string; instructions?: string; color?: string }) {
		const newProject = await projectsApi.create(data);
		projects = [...projects, newProject];
	}

	async function handleUpdateProject(data: { name?: string; path?: string; aliases?: string; instructions?: string; color?: string }) {
		if (!editingProject) return;
		const updated = await projectsApi.update(editingProject.id, data);
		projects = projects.map((p) => (p.id === editingProject!.id ? updated : p));
	}

	async function handleDeleteProject() {
		if (!editingProject) return;
		await projectsApi.delete(editingProject.id);
		projects = projects.filter((p) => p.id !== editingProject!.id);
	}
</script>

<svelte:window on:keydown={handleKeydown} />

{#if loading}
	<div class="min-h-screen bg-background flex items-center justify-center">
		<div class="flex flex-col items-center gap-4">
			<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			<p class="text-muted-foreground">Loading settings...</p>
		</div>
	</div>
{:else}
	<div class="min-h-screen bg-background">
		<div class="container mx-auto p-4 md:p-6 max-w-2xl">
			<!-- Header -->
			<div class="flex items-center gap-4 mb-8">
				<button class="btn-sm-icon-ghost" onclick={onBack}>
					<ArrowLeft class="h-5 w-5" />
				</button>
				<div>
					<h1 class="text-2xl font-bold">Settings</h1>
					<p class="text-sm text-muted-foreground">Customize your taskyou experience</p>
				</div>
			</div>

			<div class="space-y-6">
				<!-- Appearance -->
				<div class="card">
					<div class="p-6">
						<div class="flex items-center gap-2 mb-4">
							<div class="p-2 rounded-lg bg-primary/10">
								<Palette class="h-4 w-4 text-primary" />
							</div>
							<div>
								<h3 class="font-medium">Appearance</h3>
								<p class="text-xs text-muted-foreground">Customize how taskyou looks</p>
							</div>
						</div>
						<div>
							<label class="text-sm font-medium mb-2 block">Theme</label>
							<div class="grid grid-cols-3 gap-2">
								{#each ['light', 'dark', 'system'] as theme}
									{@const isActive = settings.theme === theme || (!settings.theme && theme === 'system')}
									<button
										onclick={() => (settings.theme = theme)}
										class="flex items-center justify-center gap-2 p-3 rounded-lg border-2 transition-all capitalize {isActive ? 'border-primary bg-primary/5' : 'border-transparent bg-muted/50'}"
									>
										{theme}
									</button>
								{/each}
							</div>
						</div>
					</div>
				</div>

				<!-- Defaults -->
				<div class="card">
					<div class="p-6">
						<div class="flex items-center gap-2 mb-4">
							<div class="p-2 rounded-lg bg-blue-500/10">
								<Settings2 class="h-4 w-4 text-blue-500" />
							</div>
							<div>
								<h3 class="font-medium">Defaults</h3>
								<p class="text-xs text-muted-foreground">Default values for new tasks</p>
							</div>
						</div>
						<div class="space-y-4">
							<div>
								<label class="text-sm font-medium mb-2 block">Default Project</label>
								<select
									bind:value={settings.default_project}
									class="w-full h-10 rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
								>
									<option value="personal">Personal</option>
									{#each projects as p}
										<option value={p.name}>{p.name}</option>
									{/each}
								</select>
							</div>
							<div>
								<label class="text-sm font-medium mb-2 block">Default Task Type</label>
								<select
									bind:value={settings.default_type}
									class="w-full h-10 rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
								>
									<option value="code">Code</option>
									<option value="writing">Writing</option>
									<option value="thinking">Thinking</option>
								</select>
							</div>
						</div>
					</div>
				</div>

				<!-- Projects -->
				<div class="card">
					<div class="p-6">
						<div class="flex items-center justify-between mb-4">
							<div class="flex items-center gap-2">
								<div class="p-2 rounded-lg bg-green-500/10">
									<Folder class="h-4 w-4 text-green-500" />
								</div>
								<div>
									<h3 class="font-medium">Projects</h3>
									<p class="text-xs text-muted-foreground">Manage your projects</p>
								</div>
							</div>
							<button class="btn-sm" onclick={() => (showNewProject = true)}>
								<Plus class="h-4 w-4" />
								Add
							</button>
						</div>

						{#if projects.length === 0}
							<div class="text-center py-8 text-muted-foreground">
								<Folder class="h-8 w-8 mx-auto mb-2 opacity-30" />
								<p class="text-sm">No projects yet</p>
								<button class="text-sm text-primary underline" onclick={() => (showNewProject = true)}>
									Create your first project
								</button>
							</div>
						{:else}
							<div class="space-y-2">
								{#each projects as project}
									<div class="flex items-center justify-between p-3 rounded-lg border hover:bg-muted/50 transition-colors group">
										<div class="flex items-center gap-3 min-w-0">
											<div class="w-3 h-3 rounded-full shrink-0" style:background-color={project.color || '#888'}></div>
											<div class="min-w-0">
												<div class="font-medium text-sm">{project.name}</div>
												<div class="text-xs text-muted-foreground truncate max-w-[200px]">{project.path}</div>
											</div>
										</div>
										<button
											class="px-2 py-1 text-sm rounded-md opacity-0 group-hover:opacity-100 hover:bg-muted transition-all"
											onclick={() => (editingProject = project)}
										>
											Edit
										</button>
									</div>
								{/each}
							</div>
						{/if}
					</div>
				</div>

				<!-- Data -->
				<div class="card">
					<div class="p-6">
						<div class="flex items-center gap-2 mb-4">
							<div class="p-2 rounded-lg bg-orange-500/10">
								<Download class="h-4 w-4 text-orange-500" />
							</div>
							<div>
								<h3 class="font-medium">Data</h3>
								<p class="text-xs text-muted-foreground">Export or manage your data</p>
							</div>
						</div>
						<p class="text-xs text-muted-foreground">
							Data export is available through the API.
						</p>
					</div>
				</div>

				<!-- Save -->
				<div class="flex justify-end pb-8">
					<button
						onclick={handleSave}
						disabled={saving}
						class="btn min-w-[140px] transition-all {saved ? 'bg-green-500 hover:bg-green-600 text-white border-green-500' : ''}"
					>
						{#if saving}
							<span class="h-4 w-4 border-2 border-current border-t-transparent rounded-full animate-spin"></span>
							Saving...
						{:else if saved}
							<Check class="h-4 w-4" />
							Saved!
						{:else}
							Save Settings
						{/if}
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}

{#if showNewProject}
	<ProjectDialog
		onSubmit={handleCreateProject}
		onClose={() => (showNewProject = false)}
	/>
{/if}

{#if editingProject}
	<ProjectDialog
		project={editingProject}
		onSubmit={handleUpdateProject}
		onDelete={handleDeleteProject}
		onClose={() => (editingProject = null)}
	/>
{/if}
