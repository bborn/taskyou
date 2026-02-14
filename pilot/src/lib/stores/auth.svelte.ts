import type { User } from '$lib/types';
import { auth as authApi } from '$lib/api/client';

export const authState = $state({
	user: null as User | null,
	loading: true,
});

export async function fetchUser() {
	authState.loading = true;
	try {
		authState.user = await authApi.getMe();
	} catch {
		authState.user = null;
	} finally {
		authState.loading = false;
	}
}

export async function logout() {
	try {
		await authApi.logout();
	} catch {
		// ignore
	}
	authState.user = null;
}
