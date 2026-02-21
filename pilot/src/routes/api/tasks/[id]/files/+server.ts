import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listTaskFiles } from '$lib/server/db';

// GET /api/tasks/:id/files — list files for a task
export const GET: RequestHandler = async ({ params, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	const files = await listTaskFiles(db, user.id, taskId);
	return json(files);
};
