import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { updateTask, addTaskLog } from '$lib/server/db';
import { executeInSandbox } from '$lib/server/sandbox';

// POST /api/tasks/:id/queue - Queue a task for execution
export const POST: RequestHandler = async ({ params, locals, platform }) => {
	const user = locals.user!;
	const db = platform!.env.DB;
	const taskId = parseInt(params.id);

	// Update status to queued
	const task = await updateTask(db, user.id, taskId, { status: 'queued' });
	if (!task) {
		return json({ error: 'Task not found' }, { status: 404 });
	}

	// Log the queue action
	await addTaskLog(db, taskId, 'system', `Task queued for execution`);

	// Start execution in sandbox (fire and forget)
	if (platform?.env?.SANDBOX) {
		platform.context.waitUntil(
			(async () => {
				try {
					await addTaskLog(db, taskId, 'system', 'Starting sandbox execution...');
					await updateTask(db, user.id, taskId, { status: 'processing' });

					const result = await executeInSandbox(
						platform.env.SANDBOX,
						user.id,
						`echo "Executing task: ${task.title}"`,
					);

					await addTaskLog(db, taskId, 'output', result.stdout || '');
					if (result.stderr) {
						await addTaskLog(db, taskId, 'error', result.stderr);
					}

					// Mark as blocked (needs human review) rather than auto-completing
					await updateTask(db, user.id, taskId, { status: 'blocked' });
					await addTaskLog(db, taskId, 'system', 'Task execution completed, awaiting review');
				} catch (error) {
					await addTaskLog(db, taskId, 'error', `Execution failed: ${error}`);
					await updateTask(db, user.id, taskId, { status: 'blocked' });
				}
			})(),
		);
	}

	return json(task);
};
