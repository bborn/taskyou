import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { listChats, createChat } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) return json([], { status: 401 });

	const chats = await listChats(platform.env.DB, user.id);
	return json(chats);
};

export const POST: RequestHandler = async ({ locals, platform, request }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) return json({ error: 'Unauthorized' }, { status: 401 });

	const data = await request.json() as { title?: string; model_id?: string };
	const chat = await createChat(platform.env.DB, user.id, data);
	return json(chat, { status: 201 });
};
