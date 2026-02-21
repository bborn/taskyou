<script lang="ts">
	import { FolderOpen, Plus, Pencil, Trash2, Github } from 'lucide-svelte';
	import { projects as projectsApi } from '$lib/api/client';
	import type { Project } from '$lib/types';
	import ProjectDialog from './ProjectDialog.svelte';

	let projectList = $state<Project[]>([]);
	let loading = $state(true);
	let initialized = $state(false);
	let showNewProject = $state(false);
	let editingProject = $state<Project | null>(null);

	$effect(() => {
		if (initialized) return;
		initialized = true;

		projectsApi.list().then((data) => {
			projectList = data;
		}).catch((e) => {
			console.error('Failed to load projects:', e);
		}).finally(() => {
			loading = false;
		});
	});

	async function handleCreate(data: { name: string; instructions?: string; color?: string; github_repo?: string; github_branch?: string }) {
		const project = await projectsApi.create(data);
		projectList = [project, ...projectList];
	}

	async function handleUpdate(data: { name?: string; instructions?: string; color?: string; github_repo?: string; github_branch?: string }) {
		if (!editingProject) return;
		const updated = await projectsApi.update(editingProject.id, data);
		projectList = projectList.map(p => p.id === editingProject!.id ? updated : p);
	}

	async function handleDelete() {
		if (!editingProject) return;
		await projectsApi.delete(editingProject.id);
		projectList = projectList.filter(p => p.id !== editingProject!.id);
	}
</script>

<div class="h-full overflow-y-auto p-6">
	<div class="max-w-4xl mx-auto">
		<div class="flex items-center justify-between mb-6">
			<div class="flex items-center gap-3">
				<FolderOpen class="h-6 w-6 text-primary" />
				<h1 class="text-2xl font-bold">Projects</h1>
			</div>
			<button class="btn" onclick={() => (showNewProject = true)}>
				<Plus class="h-4 w-4" />
				New Project
			</button>
		</div>

		<p class="text-muted-foreground mb-8">
			Organize your tasks into projects. Use the chat to ask Pilot to create and manage tasks.
		</p>

		{#if loading}
			<div class="flex items-center justify-center py-20">
				<div class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
			</div>
		{:else if projectList.length === 0}
			<div class="flex flex-col items-center justify-center py-20 text-center">
				<div class="p-4 rounded-full bg-muted mb-4">
					<FolderOpen class="h-8 w-8 text-muted-foreground" />
				</div>
				<h3 class="font-semibold mb-1">No Projects</h3>
				<p class="text-sm text-muted-foreground mb-4">Create a project to organize your tasks</p>
				<button class="btn" onclick={() => (showNewProject = true)}>
					<Plus class="h-4 w-4" />
					Create Project
				</button>
			</div>
		{:else}
			<div class="space-y-3">
				{#each projectList as project (project.id)}
					<div class="card p-5">
						<div class="flex items-center justify-between">
							<div class="flex items-center gap-4">
								<div class="p-2.5 rounded-lg" style="background-color: {project.color}20;">
									<FolderOpen class="h-5 w-5" style="color: {project.color};" />
								</div>
								<div>
									<h3 class="font-semibold">{project.name}</h3>
									{#if project.github_repo}
										<p class="text-xs text-muted-foreground mt-0.5 flex items-center gap-1">
											<Github class="h-3 w-3" />
											{project.github_repo}
											{#if project.github_branch}
												<span class="opacity-60">({project.github_branch})</span>
											{/if}
										</p>
									{/if}
									{#if project.instructions}
										<p class="text-xs text-muted-foreground mt-0.5 line-clamp-1">{project.instructions}</p>
									{/if}
									<p class="text-xs text-muted-foreground mt-0.5">
										Created {new Date(project.created_at).toLocaleDateString()}
									</p>
								</div>
							</div>
							<div class="flex items-center gap-2">
								<button
									class="btn-sm-icon-ghost"
									onclick={() => (editingProject = project)}
									title="Edit project"
								>
									<Pencil class="h-4 w-4 text-muted-foreground" />
								</button>
								<button
									class="btn-sm-icon-ghost"
									onclick={async () => { await projectsApi.delete(project.id); projectList = projectList.filter(p => p.id !== project.id); }}
									title="Delete project"
								>
									<Trash2 class="h-4 w-4 text-muted-foreground hover:text-destructive" />
								</button>
							</div>
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>

{#if showNewProject}
	<ProjectDialog
		onSubmit={handleCreate}
		onClose={() => (showNewProject = false)}
	/>
{/if}

{#if editingProject}
	<ProjectDialog
		project={editingProject}
		onSubmit={handleUpdate}
		onDelete={handleDelete}
		onClose={() => (editingProject = null)}
	/>
{/if}
