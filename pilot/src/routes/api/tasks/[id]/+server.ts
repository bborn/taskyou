import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getTask, updateTask, deleteTask } from '$lib/server/db';

// GET /api/tasks/:id
export const GET: RequestHandler = async ({ params, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	const task = await getTask(db, user.id, taskId);
	if (!task) {
		return json({ error: 'Task not found' }, { status: 404 });
	}

	return json(task);
};

// PUT /api/tasks/:id
export const PUT: RequestHandler = async ({ params, request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	const data = await request.json() as { title?: string; body?: string; status?: import('$lib/types').TaskStatus; type?: string; project_id?: string };
	const task = await updateTask(db, user.id, taskId, data);
	if (!task) {
		return json({ error: 'Task not found' }, { status: 404 });
	}

	return json(task);
};

// DELETE /api/tasks/:id
export const DELETE: RequestHandler = async ({ params, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	const deleted = await deleteTask(db, user.id, taskId);
	if (!deleted) {
		return json({ error: 'Task not found' }, { status: 404 });
	}

	return new Response(null, { status: 204 });
};
