import { error, text } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getTask } from '$lib/server/db';

// GET /api/tasks/:id/file?path=...
// Returns file content from a task's worktree
export const GET: RequestHandler = async ({ params, url, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);
	const filePath = url.searchParams.get('path');

	if (!filePath) throw error(400, 'Missing path parameter');

	const task = await getTask(db, user.id, taskId);
	if (!task) throw error(404, 'Task not found');

	if (!task.worktree_path) {
		throw error(404, 'Task has no worktree');
	}

	// TODO: When sandbox integration is ready, fetch file from the sandbox/worktree
	// For now return a placeholder
	return text(`// File: ${filePath}\n// Content will be available when sandbox integration is complete.\n// Worktree: ${task.worktree_path}`);
};
