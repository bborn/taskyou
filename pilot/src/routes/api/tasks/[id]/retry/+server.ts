import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { updateTask, addTaskLog } from '$lib/server/db';

// POST /api/tasks/:id/retry - Retry a task with optional feedback
export const POST: RequestHandler = async ({ params, request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	const body = await request.json().catch(() => ({}));
	const feedback = (body as { feedback?: string }).feedback;

	// Update status back to queued
	const task = await updateTask(db, user.id, taskId, { status: 'queued' });
	if (!task) {
		return json({ error: 'Task not found' }, { status: 404 });
	}

	if (feedback) {
		await addTaskLog(db, taskId, 'system', `Retrying with feedback: ${feedback}`);
	} else {
		await addTaskLog(db, taskId, 'system', 'Retrying task');
	}

	return json(task);
};
