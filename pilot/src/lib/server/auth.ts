import { findOrCreateUser } from './db';

// Session management using KV
const SESSION_TTL = 60 * 60 * 24 * 30; // 30 days

export async function createSession(
	kv: KVNamespace,
	userId: string,
): Promise<string> {
	const sessionId = crypto.randomUUID();
	await kv.put(`session:${sessionId}`, userId, { expirationTtl: SESSION_TTL });
	return sessionId;
}

export async function getSessionUserId(
	kv: KVNamespace,
	sessionId: string,
): Promise<string | null> {
	return kv.get(`session:${sessionId}`);
}

export async function deleteSession(kv: KVNamespace, sessionId: string): Promise<void> {
	await kv.delete(`session:${sessionId}`);
}

// OAuth helpers
interface GoogleUserInfo {
	id: string;
	email: string;
	name: string;
	picture: string;
}

interface GitHubUserInfo {
	id: number;
	login: string;
	name: string | null;
	email: string | null;
	avatar_url: string;
}

export async function exchangeGoogleCode(
	code: string,
	clientId: string,
	clientSecret: string,
	redirectUri: string,
): Promise<GoogleUserInfo> {
	// Exchange code for token
	const tokenRes = await fetch('https://oauth2.googleapis.com/token', {
		method: 'POST',
		headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
		body: new URLSearchParams({
			code,
			client_id: clientId,
			client_secret: clientSecret,
			redirect_uri: redirectUri,
			grant_type: 'authorization_code',
		}),
	});

	if (!tokenRes.ok) throw new Error('Failed to exchange Google OAuth code');
	const tokenData = (await tokenRes.json()) as { access_token: string };

	// Get user info
	const userRes = await fetch('https://www.googleapis.com/oauth2/v2/userinfo', {
		headers: { Authorization: `Bearer ${tokenData.access_token}` },
	});

	if (!userRes.ok) throw new Error('Failed to get Google user info');
	return userRes.json() as Promise<GoogleUserInfo>;
}

export async function exchangeGitHubCode(
	code: string,
	clientId: string,
	clientSecret: string,
): Promise<GitHubUserInfo> {
	// Exchange code for token
	const tokenRes = await fetch('https://github.com/login/oauth/access_token', {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			Accept: 'application/json',
		},
		body: JSON.stringify({
			client_id: clientId,
			client_secret: clientSecret,
			code,
		}),
	});

	if (!tokenRes.ok) throw new Error('Failed to exchange GitHub OAuth code');
	const tokenData = (await tokenRes.json()) as { access_token: string };

	// Get user info
	const userRes = await fetch('https://api.github.com/user', {
		headers: {
			Authorization: `Bearer ${tokenData.access_token}`,
			'User-Agent': 'TaskYou-Pilot',
		},
	});

	if (!userRes.ok) throw new Error('Failed to get GitHub user info');
	const user = (await userRes.json()) as GitHubUserInfo;

	// Get primary email if not public
	if (!user.email) {
		const emailRes = await fetch('https://api.github.com/user/emails', {
			headers: {
				Authorization: `Bearer ${tokenData.access_token}`,
				'User-Agent': 'TaskYou-Pilot',
			},
		});
		if (emailRes.ok) {
			const emails = (await emailRes.json()) as Array<{ email: string; primary: boolean }>;
			const primary = emails.find((e) => e.primary);
			if (primary) user.email = primary.email;
		}
	}

	return user;
}

export async function handleGoogleCallback(
	db: D1Database,
	kv: KVNamespace,
	code: string,
	clientId: string,
	clientSecret: string,
	redirectUri: string,
): Promise<{ sessionId: string; user: { id: string; email: string; name: string; avatar_url: string } }> {
	const googleUser = await exchangeGoogleCode(code, clientId, clientSecret, redirectUri);
	const user = await findOrCreateUser(db, 'google', googleUser.id, googleUser.email, googleUser.name, googleUser.picture);
	const sessionId = await createSession(kv, user.id);
	return { sessionId, user };
}

export async function handleGitHubCallback(
	db: D1Database,
	kv: KVNamespace,
	code: string,
	clientId: string,
	clientSecret: string,
): Promise<{ sessionId: string; user: { id: string; email: string; name: string; avatar_url: string } }> {
	const ghUser = await exchangeGitHubCode(code, clientId, clientSecret);
	const user = await findOrCreateUser(
		db,
		'github',
		String(ghUser.id),
		ghUser.email || `${ghUser.login}@github`,
		ghUser.name || ghUser.login,
		ghUser.avatar_url,
	);
	const sessionId = await createSession(kv, user.id);
	return { sessionId, user };
}
