import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
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
