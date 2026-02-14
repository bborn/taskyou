import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { updateProject, deleteProject } from '$lib/server/db';

// PUT /api/projects/:id
export const PUT: RequestHandler = async ({ params, request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const projectId = parseInt(params.id);
	const data = await request.json() as { name?: string; path?: string; aliases?: string; instructions?: string; color?: string };

	const project = await updateProject(db, user.id, projectId, data);
	if (!project) {
		return json({ error: 'Project not found' }, { status: 404 });
	}

	return json(project);
};

// DELETE /api/projects/:id
export const DELETE: RequestHandler = async ({ params, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const projectId = parseInt(params.id);

	const deleted = await deleteProject(db, user.id, projectId);
	if (!deleted) {
		return json({ error: 'Project not found' }, { status: 404 });
	}

	return new Response(null, { status: 204 });
};
