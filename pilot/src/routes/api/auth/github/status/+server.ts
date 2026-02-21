import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';

// GET /api/auth/github/status - Check if user has valid GitHub token
export const GET: RequestHandler = async ({ locals, platform }) => {
	if (!locals.user) throw error(401);

	const sessions = platform?.env?.SESSIONS as KVNamespace | undefined;
	if (!sessions) throw error(500, 'KV not available');

	const token = await sessions.get(`github-token:${locals.user.id}`);
	if (!token) {
		return json({ connected: false });
	}

	// Validate token against GitHub API
	const response = await fetch('https://api.github.com/user', {
		headers: {
			Authorization: `Bearer ${token}`,
			'User-Agent': 'TaskYou-Pilot',
		},
	});

	if (!response.ok) {
		// Token is invalid or expired — clean up
		await sessions.delete(`github-token:${locals.user.id}`);
		return json({ connected: false });
	}

	const user = await response.json() as { login: string; avatar_url: string };
	return json({ connected: true, login: user.login, avatar_url: user.avatar_url });
};
