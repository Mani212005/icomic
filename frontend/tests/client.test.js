import test from 'node:test';
import assert from 'node:assert/strict';
import { ApiClient } from '../src/lib/api/client.js';
import { formatCostQuote } from '../src/lib/api/types.js';

test('formatCostQuote labels configured estimates as unverified unless confirmed', () => {
	assert.equal(
		formatCostQuote({ estimated_cost: 0.0275, currency: 'USD', price_verified: false }),
		'Unverified estimate: $0.0275'
	);
	assert.equal(
		formatCostQuote({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		'Estimate: $0.01'
	);
	assert.equal(formatCostQuote(null), 'Estimate unavailable');
});

test('ApiClient initializes with default or custom baseUrl', () => {
	const defaultClient = new ApiClient();
	assert.equal(defaultClient.baseUrl, 'http://localhost:8080');

	const customClient = new ApiClient('http://custom-api:9090/');
	assert.equal(customClient.baseUrl, 'http://custom-api:9090');
});

test('ApiClient checkHealth fetches /healthz endpoint', async () => {
	const mockResponse = { status: 'ok' };
	const originalFetch = globalThis.fetch;
	try {
		globalThis.fetch = async (url) => {
			assert.equal(url, 'http://localhost:8080/healthz');
			return {
				ok: true,
				status: 200,
				json: async () => mockResponse
			};
		};

		const client = new ApiClient();
		const result = await client.checkHealth();
		assert.deepEqual(result, { status: 'ok' });
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test('ApiClient checkHealth handles HTTP error with parsed message', async () => {
	const originalFetch = globalThis.fetch;
	try {
		globalThis.fetch = async () => ({
			ok: false,
			status: 503,
			json: async () => ({ message: 'Service temporarily overloaded' })
		});

		const client = new ApiClient();
		await assert.rejects(
			async () => client.checkHealth(),
			{ message: 'Service temporarily overloaded' }
		);
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test('ApiClient uploads the coloured page as raw PNG bytes', async () => {
	const originalFetch = globalThis.fetch;
	const blob = new Blob([new Uint8Array([137, 80, 78, 71])], { type: 'image/png' });
	try {
		globalThis.fetch = async (url, options) => {
			assert.equal(url, 'http://test-api:8080/api/v1/pages/page-1/colour-image');
			assert.equal(options.method, 'PUT');
			assert.equal(options.body, blob);
			assert.equal(options.headers['Content-Type'], 'image/png');
			return { ok: true, status: 200, json: async () => ({ colour_image_url: 'stored' }) };
		};
		const client = new ApiClient('http://test-api:8080');
		assert.deepEqual(await client.uploadColourImage('page-1', blob), { colour_image_url: 'stored' });
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test('ApiClient sends correct HTTP request for projects and operations', async () => {
	const requests = [];
	const originalFetch = globalThis.fetch;

	try {
		globalThis.fetch = async (url, options) => {
			requests.push({ url, options });
			return {
				ok: true,
				status: 200,
				headers: new Map([['content-type', 'application/json']]),
				json: async () => ({ success: true })
			};
		};

		const client = new ApiClient('http://test-server:8080');

		await client.createProject({ title: 'Test Comic' });
		assert.equal(requests[0].url, 'http://test-server:8080/api/v1/projects');
		assert.equal(requests[0].options.method, 'POST');
		assert.equal(requests[0].options.body, JSON.stringify({ title: 'Test Comic' }));

		await client.estimateCost('proj-1', 'draft_sketch', 3);
		assert.equal(requests[1].url, 'http://test-server:8080/api/v1/projects/proj-1/estimate');
		assert.equal(requests[1].options.method, 'POST');

		await client.generateDraft('page-1');
		assert.equal(requests[2].url, 'http://test-server:8080/api/v1/pages/page-1/draft');
		assert.equal(requests[2].options.method, 'POST');

		await client.approveDraft('page-1');
		assert.equal(requests[3].url, 'http://test-server:8080/api/v1/pages/page-1/draft/approve');
		assert.equal(requests[3].options.method, 'POST');

		await client.suggestColourPalette('page-1');
		assert.equal(requests[4].url, 'http://test-server:8080/api/v1/pages/page-1/colour-palette/suggest');

		await client.approveColour('page-1');
		assert.equal(requests[5].url, 'http://test-server:8080/api/v1/pages/page-1/colour/approve');

		await client.getJob('job-999');
		assert.equal(requests[6].url, 'http://test-server:8080/api/v1/generation-jobs/job-999');

		await client.cancelJob('job-999');
		assert.equal(requests[7].url, 'http://test-server:8080/api/v1/generation-jobs/job-999/cancel');

		await client.exportProject('proj-1', { format: 'pdf', page_size: 'a4' });
		assert.equal(requests[8].url, 'http://test-server:8080/api/v1/projects/proj-1/export');

		const downloadUrl = client.getExportDownloadUrl('exp-1');
		assert.equal(downloadUrl, 'http://test-server:8080/api/v1/exports/exp-1/download');
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test('ApiClient identity handling matches Go API authenticator contract', async () => {
	const requests = [];
	const originalFetch = globalThis.fetch;

	try {
		globalThis.fetch = async (url, options) => {
			requests.push({ url, options });
			return {
				ok: true,
				status: 200,
				json: async () => ({ status: 'ok' })
			};
		};

		const unauthClient = new ApiClient({
			baseUrl: 'http://test-api:8080',
			devMode: false,
			devUserId: null,
			token: null
		});
		assert.equal(unauthClient.isAuthenticated(), false);
		assert.equal(unauthClient.getAuthMode(), 'unauthenticated');

		await unauthClient.checkHealth();
		const unauthHeaders = requests[0].options.headers;
		assert.equal(unauthHeaders['X-User-ID'], undefined);
		assert.equal(unauthHeaders['Authorization'], undefined);

		const devClient = new ApiClient({
			baseUrl: 'http://test-api:8080',
			devMode: true,
			devUserId: '00000000-0000-0000-0000-000000000001'
		});
		assert.equal(devClient.isAuthenticated(), true);
		assert.equal(devClient.getAuthMode(), 'dev');

		await devClient.checkHealth();
		const devHeaders = requests[1].options.headers;
		assert.equal(devHeaders['X-User-ID'], '00000000-0000-0000-0000-000000000001');
		assert.equal(devHeaders['Authorization'], undefined);

		const bearerClient = new ApiClient({
			baseUrl: 'http://test-api:8080',
			token: 'test-session-token-xyz'
		});
		assert.equal(bearerClient.isAuthenticated(), true);
		assert.equal(bearerClient.getAuthMode(), 'bearer');

		await bearerClient.checkHealth();
		const bearerHeaders = requests[2].options.headers;
		assert.equal(bearerHeaders['Authorization'], 'Bearer test-session-token-xyz');
		assert.equal(bearerHeaders['X-User-ID'], undefined);

		unauthClient.setDevMode(true, '00000000-0000-0000-0000-000000000002');
		assert.equal(unauthClient.isAuthenticated(), true);
		assert.equal(unauthClient.getAuthMode(), 'dev');

		await unauthClient.checkHealth();
		assert.equal(requests[3].options.headers['X-User-ID'], '00000000-0000-0000-0000-000000000002');
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test('ApiClient normalizes wrapped Go backend responses for projects and entities', async () => {
	const originalFetch = globalThis.fetch;

	try {
		globalThis.fetch = async (url) => {
			if (url.endsWith('/api/v1/projects')) {
				return {
					ok: true,
					status: 200,
					json: async () => ({
						projects: [{ id: 'p1', title: 'Wrapped Project' }]
					})
				};
			}
			if (url.includes('/panels')) {
				return {
					ok: true,
					status: 200,
					json: async () => ({
						panels: [{ id: 'pnl1', panel_number: 1 }]
					})
				};
			}
			if (url.includes('/pages')) {
				return {
					ok: true,
					status: 200,
					json: async () => ({
						pages: [{ id: 'pg1', page_number: 1 }]
					})
				};
			}
			if (url.includes('/characters')) {
				return {
					ok: true,
					status: 200,
					json: async () => ({
						characters: [{ id: 'c1', name: 'Maya' }]
					})
				};
			}
			if (url.includes('/chapters')) {
				return {
					ok: true,
					status: 200,
					json: async () => ({
						chapters: [{ id: 'ch1', title: 'Chapter 1' }]
					})
				};
			}
			if (url.includes('/me/credit-usage')) {
				return {
					ok: true,
					status: 200,
					json: async () => ({
						user_id: 'u1',
						balance: 75.5,
						usage_history: []
					})
				};
			}
			return { ok: true, status: 200, json: async () => ({}) };
		};

		const client = new ApiClient('http://test-api:8080');

		const projects = await client.listProjects();
		assert.ok(Array.isArray(projects), 'unwraps projects array');
		assert.equal(projects[0].id, 'p1');

		const characters = await client.getCharacters('p1');
		assert.ok(Array.isArray(characters), 'unwraps characters array');
		assert.equal(characters[0].name, 'Maya');

		const chapters = await client.getChapters('p1');
		assert.ok(Array.isArray(chapters), 'unwraps chapters array');

		const panels = await client.getPanels('ch1');
		assert.ok(Array.isArray(panels), 'unwraps panels array');

		const pages = await client.getPages('ch1');
		assert.ok(Array.isArray(pages), 'unwraps pages array');

		const credits = await client.getCreditUsage();
		assert.equal(credits.remaining_credits, 75.5, 'normalizes remaining_credits from balance');
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test('ApiClient normalizes script aliases without sending unknown fields', async () => {
	const client = new ApiClient('http://test-server');
	const requests = [];
	client.request = async (path, options) => { requests.push(JSON.parse(options.body)); return {}; };
	await client.createProject({ title: 'Story', script: 'The original script' });
	await client.updateProject('project', { script: 'The edited script' });
	assert.deepEqual(requests, [
	 { title: 'Story', original_script: 'The original script' },
	 { original_script: 'The edited script' }
	]);
});
