import { sveltekit } from '@sveltejs/kit/vite';
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
					if (source === '$lib/api/index.js' || /\/api\/index\.js$/.test(source)) {
						return new URL('./src/lib/api/demoIndex.js', import.meta.url).pathname;
					}
				}
			}]
			: []),
		sveltekit()
	],
	server: {
		port: 5173,
		host: true
	}
});
