import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getWorkspace, updateWorkspace, deleteWorkspace } from '$lib/server/db';

export const GET: RequestHandler = async ({ params, platform }) => {
	const db = platform!.env.DB;
	const workspace = await getWorkspace(db, params.id);
	if (!workspace) {
		return json({ error: 'Workspace not found' }, { status: 404 });
	}
	return json(workspace);
};

export const PUT: RequestHandler = async ({ params, request, platform }) => {
	const db = platform!.env.DB;
	const data = await request.json() as { name?: string; autonomous_enabled?: boolean; weekly_budget_cents?: number };

	const workspace = await updateWorkspace(db, params.id, data);
	if (!workspace) {
		return json({ error: 'Workspace not found' }, { status: 404 });
	}
	return json(workspace);
};

export const DELETE: RequestHandler = async ({ params, platform }) => {
	const db = platform!.env.DB;

	if (params.id === 'default') {
		return json({ error: 'Cannot delete the default workspace' }, { status: 400 });
	}

	const deleted = await deleteWorkspace(db, params.id);
	if (!deleted) {
		return json({ error: 'Workspace not found' }, { status: 404 });
	}
	return new Response(null, { status: 204 });
};
