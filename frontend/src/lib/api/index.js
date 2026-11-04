import { ApiClient } from './client.js';
import { MockApiClient } from './mockClient.js';

function isDemoBuild() {
	return (
		(typeof __ICOMIC_DEMO_BUILD__ !== 'undefined' && __ICOMIC_DEMO_BUILD__) ||
		(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_USE_MOCK_API === 'true') ||
		(typeof process !== 'undefined' && process.env?.PUBLIC_USE_MOCK_API === 'true')
	);
}

export * from './types.js';
export * from './client.js';
export * from './mockClient.js';
export * from './jobPolling.js';

export function getApiConfig() {
	if (isDemoBuild()) {
		return { useMock: true, apiUrl: '', devMode: false, devUserId: null, authToken: null };
	}

	const isMockExplicit =
		(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_USE_MOCK_API === 'true') ||
		(typeof process !== 'undefined' && process.env?.PUBLIC_USE_MOCK_API === 'true');

	const apiUrl =
		(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_API_URL) ||
		(typeof process !== 'undefined' && process.env?.PUBLIC_API_URL) ||
		'http://localhost:8080';

	const devMode =
		(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_DEV_MODE === 'true') ||
		(typeof process !== 'undefined' && process.env?.PUBLIC_DEV_MODE === 'true') ||
		false;

	const devUserId =
		(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_DEV_USER_ID) ||
		(typeof process !== 'undefined' && process.env?.PUBLIC_DEV_USER_ID) ||
		(devMode ? '00000000-0000-0000-0000-000000000001' : null);

	const authToken =
		(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_AUTH_TOKEN) ||
		(typeof process !== 'undefined' && process.env?.PUBLIC_AUTH_TOKEN) ||
		null;

	return {
		useMock: Boolean(isMockExplicit),
		apiUrl,
		devMode: devMode || Boolean(devUserId),
		devUserId,
		authToken
	};
}

export function createDefaultClient(options = {}) {
	if (isDemoBuild()) {
		return new MockApiClient(options);
	}

	const config = getApiConfig();
	const useMock = options.useMock !== undefined ? options.useMock : config.useMock;

	if (useMock) {
		return new MockApiClient(options);
	}

	return new ApiClient({
		baseUrl: options.baseUrl || config.apiUrl,
		devMode: options.devMode !== undefined ? options.devMode : config.devMode,
		devUserId: options.devUserId !== undefined ? options.devUserId : config.devUserId,
		token: options.token !== undefined ? options.token : config.authToken
	});
}

export const defaultClient = createDefaultClient();
