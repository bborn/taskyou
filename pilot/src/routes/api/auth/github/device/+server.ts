import { json, error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';

// POST /api/auth/github/device - Start GitHub device flow
export const POST: RequestHandler = async ({ locals, platform }) => {
	if (!locals.user) throw error(401);

	const clientId = platform?.env?.GITHUB_CLIENT_ID;
	if (!clientId) throw error(500, 'GitHub OAuth not configured');

	const response = await fetch('https://github.com/login/device/code', {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			Accept: 'application/json',
		},
		body: JSON.stringify({
			client_id: clientId,
			scope: 'repo',
		}),
	});

	if (!response.ok) {
		const text = await response.text();
		throw error(502, `GitHub device flow failed: ${text}`);
	}

	const data = await response.json() as {
		device_code: string;
		user_code: string;
		verification_uri: string;
		expires_in: number;
		interval: number;
	};

	return json({
		device_code: data.device_code,
		user_code: data.user_code,
		verification_uri: data.verification_uri,
		expires_in: data.expires_in,
		interval: data.interval,
	});
};
