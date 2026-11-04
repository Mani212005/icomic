import { MockApiClient } from './mockClient.js';
export { formatApiError } from './types.js';

export function createDefaultClient() {
	return new MockApiClient();
}
