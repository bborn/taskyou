import type { Project, CreateProjectRequest, UpdateProjectRequest } from '$lib/types';
import { projects as projectsApi } from '$lib/api/client';
import { navState } from './nav.svelte';

export const projectState = $state({
	projects: [] as Project[],
	loading: false,
	expandedProjects: new Set<string>(),
});

export async function fetchProjects() {
	projectState.loading = true;
	try {
		projectState.projects = await projectsApi.list();
	} catch (e) {
		console.error('Failed to fetch projects:', e);
	} finally {
		projectState.loading = false;
	}
}

export async function createProject(data: CreateProjectRequest): Promise<Project> {
	const project = await projectsApi.create(data);
	projectState.projects = [project, ...projectState.projects];
	return project;
}

export async function updateProject(id: string, data: UpdateProjectRequest): Promise<Project> {
	const updated = await projectsApi.update(id, data);
	projectState.projects = projectState.projects.map(p => p.id === id ? updated : p);
	return updated;
}

export async function deleteProject(id: string): Promise<void> {
	await projectsApi.delete(id);
	projectState.projects = projectState.projects.filter(p => p.id !== id);
	if (navState.activeProjectId === id) {
		navState.activeProjectId = null;
	}
}

export function toggleProjectExpanded(id: string) {
	const next = new Set(projectState.expandedProjects);
	if (next.has(id)) {
		next.delete(id);
	} else {
		next.add(id);
	}
	projectState.expandedProjects = next;
}

export function getActiveProject(): Project | null {
	if (!navState.activeProjectId) return null;
	return projectState.projects.find(p => p.id === navState.activeProjectId) ?? null;
}
