import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listIntegrations } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform }) => {
	if (!locals.user || !platform?.env?.DB) return json([], { status: 401 });

	const integrations = await listIntegrations(platform.env.DB);
	return json(integrations);
};
