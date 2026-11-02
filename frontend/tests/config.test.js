import test from 'node:test';
import assert from 'node:assert/strict';
import { getApiConfig, createDefaultClient } from '../src/lib/api/index.js';
import { ApiClient } from '../src/lib/api/client.js';
import { MockApiClient } from '../src/lib/api/mockClient.js';

test('getApiConfig returns expected defaults when no env variables are set', () => {
	const config = getApiConfig();
	assert.equal(typeof config.useMock, 'boolean');
	assert.equal(typeof config.apiUrl, 'string');
	assert.ok(config.apiUrl.startsWith('http'));
});

test('createDefaultClient instantiates ApiClient by default', () => {
	const client = createDefaultClient({ useMock: false });
	assert.ok(client instanceof ApiClient);
	assert.equal(client instanceof MockApiClient, false);
});

test('createDefaultClient instantiates MockApiClient only when useMock is true', () => {
	const mockClient = createDefaultClient({ useMock: true });
	assert.ok(mockClient instanceof MockApiClient);
	assert.equal(mockClient instanceof ApiClient, false);
});

test('ApiClient respects custom options for baseUrl and dev credentials', () => {
	const client = new ApiClient({
		baseUrl: 'http://custom-host:8080/',
		devMode: true,
		devUserId: '00000000-0000-0000-0000-000000000001'
	});
	assert.equal(client.baseUrl, 'http://custom-host:8080');
	assert.equal(client.isAuthenticated(), true);
	assert.equal(client.getAuthMode(), 'dev');
});
