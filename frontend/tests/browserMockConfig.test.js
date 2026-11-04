import test from 'node:test';
import assert from 'node:assert/strict';
import { build, loadEnv } from 'vite';
import { MockApiClient } from '../src/lib/api/mockClient.js';

test('browser build exposes PUBLIC_USE_MOCK_API and selects MockApiClient', async () => {
	const root = new URL('..', import.meta.url).pathname;
	const previousValue = process.env.PUBLIC_USE_MOCK_API;
	process.env.PUBLIC_USE_MOCK_API = 'true';

	try {
		const env = loadEnv('production', root, ['VITE_', 'PUBLIC_']);
		assert.equal(env.PUBLIC_USE_MOCK_API, 'true');

		const output = [];
		await build({
			configFile: `${root}/vite.config.js`,
			root,
			mode: 'production',
			logLevel: 'silent',
			build: { write: false, minify: false, rollupOptions: { output: { format: 'es' } } },
			plugins: [
				{
					name: 'capture-browser-bundle',
					generateBundle(_options, bundle) {
						output.push(...Object.values(bundle).filter((item) => item.type === 'chunk').map((item) => item.code));
					}
				}
			]
		});

		assert.ok(output.length > 0);
		assert.ok(output.some((code) => code.includes('PUBLIC_USE_MOCK_API')));

		const configModule = await import('../src/lib/api/index.js?browser-mock-test');
		assert.ok(configModule.createDefaultClient({ useMock: configModule.getApiConfig().useMock }) instanceof MockApiClient);
	} finally {
		if (previousValue === undefined) delete process.env.PUBLIC_USE_MOCK_API;
		else process.env.PUBLIC_USE_MOCK_API = previousValue;
	}
});
