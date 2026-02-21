import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';

// POST /api/auth/github/device/poll - Poll for device flow completion
export const POST: RequestHandler = async ({ request, locals, platform }) => {
	if (!locals.user) throw error(401);

	const env = platform?.env;
	const clientId = env?.GITHUB_CLIENT_ID;
	if (!clientId) throw error(500, 'GitHub OAuth not configured');

	const { device_code } = await request.json() as { device_code: string };
	if (!device_code) throw error(400, 'device_code required');

	const response = await fetch('https://github.com/login/oauth/access_token', {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			Accept: 'application/json',
		},
		body: JSON.stringify({
			client_id: clientId,
			device_code,
			grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
		}),
	});

	if (!response.ok) {
		throw error(502, 'GitHub token exchange failed');
	}

	const data = await response.json() as {
		access_token?: string;
		token_type?: string;
		scope?: string;
		error?: string;
		error_description?: string;
	};

	if (data.error) {
		// authorization_pending and slow_down are expected during polling
		if (data.error === 'authorization_pending' || data.error === 'slow_down') {
			return json({ status: 'pending' });
		}
		return json({ status: 'error', error: data.error, error_description: data.error_description });
	}

	if (!data.access_token) {
		return json({ status: 'error', error: 'no_token' });
	}

	// Store token in KV keyed by user ID
	const sessions = env?.SESSIONS as KVNamespace | undefined;
	if (!sessions) throw error(500, 'KV not available');

	await sessions.put(`github-token:${locals.user.id}`, data.access_token, {
		expirationTtl: 365 * 24 * 60 * 60, // 1 year
	});

	return json({ status: 'complete' });
};
