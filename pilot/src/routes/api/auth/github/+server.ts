import { redirect } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { handleGitHubCallback } from '$lib/server/auth';

// GET /api/auth/github - Start GitHub OAuth flow
export const GET: RequestHandler = async ({ url, platform, cookies }) => {
	const env = platform?.env;
	if (!env?.GITHUB_CLIENT_ID || !env?.GITHUB_CLIENT_SECRET) {
		return new Response('GitHub OAuth not configured', { status: 500 });
	}

	const code = url.searchParams.get('code');

	if (!code) {
		// Redirect to GitHub OAuth
		const authUrl = new URL('https://github.com/login/oauth/authorize');
		authUrl.searchParams.set('client_id', env.GITHUB_CLIENT_ID);
		authUrl.searchParams.set('redirect_uri', `${url.origin}/api/auth/github`);
		authUrl.searchParams.set('scope', 'user:email');
		throw redirect(302, authUrl.toString());
	}

	// Handle callback
	const { sessionId } = await handleGitHubCallback(
		env.DB,
		env.SESSIONS,
		code,
		env.GITHUB_CLIENT_ID,
		env.GITHUB_CLIENT_SECRET,
	);

	cookies.set('session', sessionId, {
		path: '/',
		httpOnly: true,
		secure: url.protocol === 'https:',
		sameSite: 'lax',
		maxAge: 60 * 60 * 24 * 30,
	});

	throw redirect(302, '/');
};
