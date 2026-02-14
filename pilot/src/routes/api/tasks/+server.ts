import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listTasks, createTask } from '$lib/server/db';

// GET /api/tasks
export const GET: RequestHandler = async ({ url, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;

	const options = {
		status: url.searchParams.get('status') || undefined,
		project: url.searchParams.get('project') || undefined,
		type: url.searchParams.get('type') || undefined,
		includeClosed: url.searchParams.get('all') === 'true',
	};

	const tasks = await listTasks(db, user.id, options);
	return json(tasks);
};

// POST /api/tasks
export const POST: RequestHandler = async ({ request, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;

	const data = await request.json() as { title?: string; body?: string; type?: string; project?: string };

	if (!data.title) {
		return json({ error: 'Title is required' }, { status: 400 });
	}

	const task = await createTask(db, user.id, {
		title: data.title,
		body: data.body,
		type: data.type,
		project: data.project,
	});

	return json(task, { status: 201 });
};
