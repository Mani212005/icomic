import test from 'node:test';
import assert from 'node:assert/strict';
import { ApiClient } from '../src/lib/api/client.js';

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

		// 1. Create project
		await client.createProject({ title: 'Test Comic' });
		assert.equal(requests[0].url, 'http://test-server:8080/api/v1/projects');
		assert.equal(requests[0].options.method, 'POST');
		assert.equal(requests[0].options.body, JSON.stringify({ title: 'Test Comic' }));

		// 2. Estimate cost
		await client.estimateCost('proj-1', 'draft_sketch', 3);
		assert.equal(requests[1].url, 'http://test-server:8080/api/v1/projects/proj-1/estimate');
		assert.equal(requests[1].options.method, 'POST');

		// 3. Draft generation and approval
		await client.generateDraft('page-1');
		assert.equal(requests[2].url, 'http://test-server:8080/api/v1/pages/page-1/draft');
		assert.equal(requests[2].options.method, 'POST');

		await client.approveDraft('page-1');
		assert.equal(requests[3].url, 'http://test-server:8080/api/v1/pages/page-1/draft/approve');
		assert.equal(requests[3].options.method, 'POST');

		// 4. Colour palette and approval
		await client.suggestColourPalette('page-1');
		assert.equal(requests[4].url, 'http://test-server:8080/api/v1/pages/page-1/colour-palette/suggest');

		await client.approveColour('page-1');
		assert.equal(requests[5].url, 'http://test-server:8080/api/v1/pages/page-1/colour/approve');

		// 5. Job inspection and cancel
		await client.getJob('job-999');
		assert.equal(requests[6].url, 'http://test-server:8080/api/v1/generation-jobs/job-999');

		await client.cancelJob('job-999');
		assert.equal(requests[7].url, 'http://test-server:8080/api/v1/generation-jobs/job-999/cancel');

		// 6. Export
		await client.exportProject('proj-1', { format: 'pdf', page_size: 'a4' });
		assert.equal(requests[8].url, 'http://test-server:8080/api/v1/projects/proj-1/export');

		const downloadUrl = client.getExportDownloadUrl('exp-1');
		assert.equal(downloadUrl, 'http://test-server:8080/api/v1/exports/exp-1/download');
	} finally {
		globalThis.fetch = originalFetch;
	}
});
