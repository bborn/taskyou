import { redirect } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { handleGoogleCallback } from '$lib/server/auth';

// GET /api/auth/google - Start Google OAuth flow
export const GET: RequestHandler = async ({ url, platform, cookies }) => {
	const env = platform?.env;
	if (!env?.GOOGLE_CLIENT_ID || !env?.GOOGLE_CLIENT_SECRET) {
		return new Response('Google OAuth not configured', { status: 500 });
	}

	const code = url.searchParams.get('code');
	const redirectUri = `${url.origin}/api/auth/google`;

	if (!code) {
		// Redirect to Google OAuth
		const authUrl = new URL('https://accounts.google.com/o/oauth2/v2/auth');
		authUrl.searchParams.set('client_id', env.GOOGLE_CLIENT_ID);
		authUrl.searchParams.set('redirect_uri', redirectUri);
		authUrl.searchParams.set('response_type', 'code');
		authUrl.searchParams.set('scope', 'openid email profile');
		authUrl.searchParams.set('access_type', 'offline');
		throw redirect(302, authUrl.toString());
	}

	// Handle callback
	const { sessionId } = await handleGoogleCallback(
		env.DB,
		env.SESSIONS,
		code,
		env.GOOGLE_CLIENT_ID,
		env.GOOGLE_CLIENT_SECRET,
		redirectUri,
	);

	cookies.set('session', sessionId, {
		path: '/',
		httpOnly: true,
		secure: url.protocol === 'https:',
		sameSite: 'lax',
		maxAge: 60 * 60 * 24 * 30, // 30 days
	});

	throw redirect(302, '/');
};
