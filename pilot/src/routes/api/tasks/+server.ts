import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listTasks, createTask } from '$lib/server/db';

// GET /api/tasks
export const GET: RequestHandler = async ({ url, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;

	const options = {
		status: url.searchParams.get('status') || undefined,
		project_id: url.searchParams.get('project_id') || undefined,
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

	const data = await request.json() as { title?: string; body?: string; type?: string; project_id?: string; chat_id?: string };

	if (!data.title) {
		return json({ error: 'Title is required' }, { status: 400 });
	}

	try {
		const task = await createTask(db, user.id, {
			title: data.title,
			body: data.body,
			type: data.type,
			project_id: data.project_id,
			chat_id: data.chat_id,
		});

		return json(task, { status: 201 });
	} catch (e) {
		console.error('Task creation failed:', e);
		return json({ error: e instanceof Error ? e.message : String(e) }, { status: 500 });
	}
};
