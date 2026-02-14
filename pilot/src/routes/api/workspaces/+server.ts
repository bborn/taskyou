import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listWorkspaces, createWorkspace } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const workspaces = await listWorkspaces(db, user.id);
	return json(workspaces);
};

export const POST: RequestHandler = async ({ request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const data = await request.json() as { name?: string };

	if (!data.name) {
		return json({ error: 'Name is required' }, { status: 400 });
	}

	const workspace = await createWorkspace(db, user.id, { name: data.name });
	return json(workspace, { status: 201 });
};
