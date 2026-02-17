import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getProjectById, updateProject, deleteProject } from '$lib/server/db';

// GET /api/projects/:id
export const GET: RequestHandler = async ({ params, locals, platform }) => {
	if (!locals.user || !platform?.env?.DB) throw error(401);

	const project = await getProjectById(platform.env.DB, params.id);
	if (!project) throw error(404, 'Project not found');
	return json(project);
};

// PUT /api/projects/:id
export const PUT: RequestHandler = async ({ params, request, locals, platform }) => {
	if (!locals.user || !platform?.env?.DB) throw error(401);

	const data = await request.json() as { name?: string; instructions?: string; color?: string };
	const project = await updateProject(platform.env.DB, params.id, data);
	if (!project) throw error(404, 'Project not found');
	return json(project);
};

// DELETE /api/projects/:id
export const DELETE: RequestHandler = async ({ params, locals, platform }) => {
	if (!locals.user || !platform?.env?.DB) throw error(401);

	const deleted = await deleteProject(platform.env.DB, params.id);
	if (!deleted) throw error(404, 'Project not found');
	return new Response(null, { status: 204 });
};
