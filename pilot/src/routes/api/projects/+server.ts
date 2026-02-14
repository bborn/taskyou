import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listProjects, createProject } from '$lib/server/db';

// GET /api/projects
export const GET: RequestHandler = async ({ locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const projects = await listProjects(db, user.id);
	return json(projects);
};

// POST /api/projects
export const POST: RequestHandler = async ({ request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const data = await request.json() as { name?: string; path?: string; aliases?: string; instructions?: string; color?: string };

	if (!data.name) {
		return json({ error: 'Name is required' }, { status: 400 });
	}

	const project = await createProject(db, user.id, { name: data.name!, path: data.path || '', ...data });
	return json(project, { status: 201 });
};
