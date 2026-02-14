import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { getChat, listMessages } from '$lib/server/db';

export const GET: RequestHandler = async ({ locals, platform, params }) => {
	const user = locals.user;
	if (!user || !platform?.env?.DB) return json([], { status: 401 });

	// Verify chat belongs to user
	const chat = await getChat(platform.env.DB, user.id, params.id);
	if (!chat) return json({ error: 'Not found' }, { status: 404 });

	const messages = await listMessages(platform.env.DB, params.id);
	return json(messages);
};
