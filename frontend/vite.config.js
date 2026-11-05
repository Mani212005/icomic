import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
	envPrefix: ['VITE_', 'PUBLIC_'],
	define: {
		__ICOMIC_DEMO_BUILD__: JSON.stringify(process.env.PUBLIC_USE_MOCK_API === 'true')
	},
	plugins: [
		...(process.env.PUBLIC_USE_MOCK_API === 'true'
			? [{
				name: 'icomic-demo-api-entry',
				enforce: 'pre',
				resolveId(source) {
					if (source === '#lib/api/index.js' || /\/api\/index\.js$/.test(source)) {
						return new URL('./src/lib/api/demoIndex.js', import.meta.url).pathname;
					}
				}
			}]
			: []),
		sveltekit({
			adapter: adapter({ fallback: '200.html' }),
			preprocess: vitePreprocess()
		})
	],
	server: {
		port: 5173,
		host: true
	}
});
