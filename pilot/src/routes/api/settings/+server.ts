import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getSettings, updateSettings } from '$lib/server/db';

// GET /api/settings
export const GET: RequestHandler = async ({ locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const settings = await getSettings(db, user.id);
	return json(settings);
};

// PUT /api/settings
export const PUT: RequestHandler = async ({ request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const data = await request.json();

	await updateSettings(db, user.id, data);
	return json({ success: true });
};
