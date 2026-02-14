import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listModels } from '$lib/server/db';

export const GET: RequestHandler = async ({ platform }) => {
	if (!platform?.env?.DB) return json([]);

	const models = await listModels(platform.env.DB);
	return json(models);
};
