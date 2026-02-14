import type { Handle } from '@sveltejs/kit';
import { getSessionUserId } from '$lib/server/auth';
import { getUserById, initHostDB } from '$lib/server/db';

let dbInitialized = false;

export const handle: Handle = async ({ event, resolve }) => {
	const platform = event.platform;

	// Initialize DB on first request
	if (platform?.env?.DB && !dbInitialized) {
		try {
			await initHostDB(platform.env.DB);
			dbInitialized = true;
		} catch (e) {
			// Table already exists is fine
			dbInitialized = true;
		}
	}

	// Dev mode: auto-authenticate with mock user
	if (platform?.env?.ENVIRONMENT === 'development' || !platform?.env?.DB) {
		const devUser = {
			id: 'dev-user',
			email: 'dev@localhost',
			name: 'Development User',
			avatar_url: '',
		};
		// Ensure dev user exists in DB
		if (platform?.env?.DB && dbInitialized) {
			try {
				await platform.env.DB.prepare(
					`INSERT OR IGNORE INTO users (id, email, name, avatar_url, provider, provider_id) VALUES (?, ?, ?, ?, ?, ?)`
				).bind(devUser.id, devUser.email, devUser.name, devUser.avatar_url, 'dev', 'dev').run();
			} catch {
				// ignore if already exists
			}
		}
		event.locals.user = devUser;
		event.locals.sessionId = 'dev-session';
		return resolve(event);
	}

	// Extract session from cookie
	const sessionId = event.cookies.get('session');
	if (sessionId && platform?.env?.SESSIONS && platform?.env?.DB) {
		const userId = await getSessionUserId(platform.env.SESSIONS, sessionId);
		if (userId) {
			const user = await getUserById(platform.env.DB, userId);
			if (user) {
				event.locals.user = user;
				event.locals.sessionId = sessionId;
			}
		}
	}

	// Protect API routes (except auth endpoints)
	const path = event.url.pathname;
	if (path.startsWith('/api/') && !path.startsWith('/api/auth/') && !event.locals.user) {
		return new Response(JSON.stringify({ error: 'Unauthorized' }), {
			status: 401,
			headers: { 'Content-Type': 'application/json' },
		});
	}

	return resolve(event);
};
