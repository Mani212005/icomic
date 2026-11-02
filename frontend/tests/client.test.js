import test from 'node:test';
import assert from 'node:assert/strict';
import { ApiClient } from '../src/lib/api/client.js';

test('ApiClient initializes with default or custom baseUrl', () => {
	const defaultClient = new ApiClient();
	assert.equal(defaultClient.baseUrl, 'http://localhost:8080');

	const customClient = new ApiClient('http://custom-api:9090');
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

test('ApiClient checkHealth handles HTTP error', async () => {
	const originalFetch = globalThis.fetch;
	try {
		globalThis.fetch = async () => ({
			ok: false,
			status: 503
		});

		const client = new ApiClient();
		await assert.rejects(
			async () => {
				await client.checkHealth();
			},
			{ message: 'API error 503' }
		);
	} finally {
		globalThis.fetch = originalFetch;
	}
});
