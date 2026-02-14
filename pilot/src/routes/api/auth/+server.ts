import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { deleteSession } from '$lib/server/auth';

// GET /api/auth - Get current user
export const GET: RequestHandler = async ({ locals }) => {
	if (!locals.user) {
		return json({ error: 'Unauthorized' }, { status: 401 });
	}
	return json(locals.user);
};

// POST /api/auth/logout
export const POST: RequestHandler = async ({ locals, cookies, platform }) => {
	if (locals.sessionId && platform?.env?.SESSIONS) {
		await deleteSession(platform.env.SESSIONS, locals.sessionId);
	}
	cookies.delete('session', { path: '/' });
	return json({ success: true });
};
