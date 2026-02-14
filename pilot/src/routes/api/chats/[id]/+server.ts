import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getChat, updateChat, deleteChat } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform, params }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) return json({ error: 'Unauthorized' }, { status: 401 });

	const chat = await getChat(platform.env.DB, user.id, params.id);
	if (!chat) return json({ error: 'Not found' }, { status: 404 });
	return json(chat);
};

export const PUT: RequestHandler = async ({ locals, platform, params, request }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) return json({ error: 'Unauthorized' }, { status: 401 });

	const data = await request.json() as { title?: string; model_id?: string };
	const chat = await updateChat(platform.env.DB, user.id, params.id, data);
	if (!chat) return json({ error: 'Not found' }, { status: 404 });
	return json(chat);
};

export const DELETE: RequestHandler = async ({ locals, platform, params }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) return json({ error: 'Unauthorized' }, { status: 401 });

	await deleteChat(platform.env.DB, user.id, params.id);
	return new Response(null, { status: 204 });
};
