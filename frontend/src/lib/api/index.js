import { ApiClient } from './client.js';
import { MockApiClient } from './mockClient.js';

export * from './types.js';
export * from './client.js';
export * from './mockClient.js';
export * from './jobPolling.js';

// Default instance uses MockApiClient if window is present and dev/mock mode is active,
// or ApiClient if talking to real backend.
export function createDefaultClient(useMock = true) {
	return useMock ? new MockApiClient() : new ApiClient();
}

export const defaultClient = new MockApiClient();
