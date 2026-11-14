import { MockApiClient } from './mockClient.js';
export { formatApiError, publishSpend, spendStore, spendView } from './types.js';

export function createDefaultClient() {
	return new MockApiClient({ accounts: true });
}
