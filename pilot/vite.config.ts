import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit()],
	resolve: {
		conditions: ['workerd', 'worker', 'browser'],
	},
	ssr: {
		resolve: {
			conditions: ['workerd', 'worker', 'node'],
			externalConditions: ['workerd', 'worker', 'node'],
		},
	},
});
