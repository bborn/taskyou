import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listProjects, createProject, updateProject } from '$lib/server/db';

// GET /api/projects
export const GET: RequestHandler = async ({ locals, platform }) => {
	if (!locals.user || !platform?.env?.DB) return json([], { status: 401 });
	const projects = await listProjects(platform.env.DB, locals.user.id);
	return json(projects);
};

// POST /api/projects — create project record (user clicks Start to provision)
export const POST: RequestHandler = async ({ locals, platform, request }) => {
	if (!locals.user || !platform?.env?.DB) return json({ error: 'Unauthorized' }, { status: 401 });

	const data = (await request.json()) as { name?: string; instructions?: string; color?: string };
	const project = await createProject(platform.env.DB, locals.user.id, data);
	return json(project, { status: 201 });
};
