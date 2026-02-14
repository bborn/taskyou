import { writable } from 'svelte/store';
import type { User } from '$lib/types';
import { auth } from '$lib/api/client';

export const user = writable<User | null>(null);
export const loading = writable(true);

export async function fetchUser() {
	loading.set(true);
	try {
		const userData = await auth.getMe();
		user.set(userData);
	} catch {
		user.set(null);
	} finally {
		loading.set(false);
	}
}

export async function logout() {
	try {
		await auth.logout();
	} catch {
		// ignore
	}
	user.set(null);
}
