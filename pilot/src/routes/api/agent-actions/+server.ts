import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listAgentActions } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform, url }) => {
	if (!locals.user || !platform?.env?.DB) return json([], { status: 401 });

	const limit = parseInt(url.searchParams.get('limit') || '50');
	const offset = parseInt(url.searchParams.get('offset') || '0');

	const actions = await listAgentActions(platform.env.DB, { limit, offset });
	return json(actions);
};
