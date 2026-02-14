import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getTaskLogs } from '$lib/server/db';

// GET /api/tasks/:id/logs
export const GET: RequestHandler = async ({ params, url, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);
	const limit = parseInt(url.searchParams.get('limit') || '200');

	const logs = await getTaskLogs(db, user.id, taskId, limit);
	return json(logs);
};
