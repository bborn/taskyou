import type { RequestHandler } from './$types';
import { getTask } from '$lib/server/db';

// GET /api/tasks/:id/file?path=foo.js — read file content from R2 storage
export const GET: RequestHandler = async ({ params, url, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);
	const filePath = url.searchParams.get('path');

	if (!filePath) {
		return new Response(JSON.stringify({ error: 'path parameter required' }), {
			status: 400,
			headers: { 'Content-Type': 'application/json' },
		});
	}

	// Verify task belongs to user
	const task = await getTask(db, user.id, taskId);
	if (!task) {
		return new Response(JSON.stringify({ error: 'Task not found' }), {
			status: 404,
			headers: { 'Content-Type': 'application/json' },
		});
	}

	const storage = platform!.env.STORAGE as R2Bucket;
	const r2Key = `tasks/${taskId}/${filePath}`;
	const object = await storage.get(r2Key);

	if (!object) {
		return new Response(JSON.stringify({ error: 'File not found' }), {
			status: 404,
			headers: { 'Content-Type': 'application/json' },
		});
	}

	const contentType = object.httpMetadata?.contentType || 'text/plain';
	return new Response(object.body, {
		headers: { 'Content-Type': `${contentType}; charset=utf-8` },
	});
};
