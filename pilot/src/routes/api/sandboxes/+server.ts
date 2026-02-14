import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listSandboxes, createSandbox } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform }) => {
	if (!locals.user || !platform?.env?.DB) return json([], { status: 401 });

	const sandboxes = await listSandboxes(platform.env.DB);
	return json(sandboxes);
};

export const POST: RequestHandler = async ({ locals, platform, request }) => {
	if (!locals.user || !platform?.env?.DB) return json({ error: 'Unauthorized' }, { status: 401 });

	const data = await request.json() as { name?: string; provider?: string };
	const sandbox = await createSandbox(platform.env.DB, data);
	return json(sandbox, { status: 201 });
};
