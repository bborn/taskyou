import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { updateTask, addTaskLog } from '$lib/server/db';

// POST /api/tasks/:id/close - Mark a task as done
export const POST: RequestHandler = async ({ params, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	const task = await updateTask(db, user.id, taskId, { status: 'done' });
	if (!task) {
		return json({ error: 'Task not found' }, { status: 404 });
	}

	await addTaskLog(db, taskId, 'system', 'Task marked as done');

	return json(task);
};
