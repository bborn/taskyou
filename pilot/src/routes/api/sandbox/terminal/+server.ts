import type { RequestHandler } from './$types';
import { handleTerminalUpgrade } from '$lib/server/sandbox';

// GET /api/sandbox/terminal - WebSocket upgrade for terminal access
export const GET: RequestHandler = async ({ request, locals, platform }) => {
	const user = locals.user!;

	if (!platform?.env?.SANDBOX) {
		return new Response('Sandbox not available', { status: 503 });
	}

	return handleTerminalUpgrade(platform.env.SANDBOX, user.id, request);
};
