import test from 'node:test';
import assert from 'node:assert/strict';
import { get } from 'svelte/store';
import { ApiClient } from '../src/lib/api/client.js';
import { formatApiError } from '../src/lib/api/types.js';
import { publishSpend, spendStore, spendView } from '../src/lib/api/spend.js';

const spend = (cap, spent, left) => ({ enabled: true, cap_usd: cap, spent_usd: spent, remaining_usd: left });

test('the header shows the running total, the cap and what is left', () => {
	const view = spendView(spend(2, 0.37, 1.63));
	assert.equal(view.text, '$0.37 / $2.00');
	assert.equal(view.title, 'Spent $0.37 of the $2.00 cap. $1.63 left.');
	assert.equal(view.level, 'ok');
});

test('a cost is never understated: spend rounds up and what is left rounds down', () => {
	const view = spendView(spend(2, 0.2071, 1.7929));
	assert.equal(view.text, '$0.21 / $2.00');
	assert.match(view.title, /\$1\.79 left\./);
	assert.equal(spendView(spend(2, 0.01, 1.99)).text, '$0.01 / $2.00');
	assert.equal(spendView(spend(2, 0, 2)).text, '$0.00 / $2.00');
});

test('the pill warns below a quarter of the cap and says so once the cap is reached', () => {
	assert.equal(spendView(spend(2, 1.6, 0.4)).level, 'low');
	assert.equal(spendView(spend(2, 1.4, 0.6)).level, 'ok');
	const out = spendView(spend(2, 2, 0));
	assert.equal(out.level, 'out');
	assert.match(out.title, /^Spend cap reached: \$2\.00 of \$2\.00\. No further paid call is sent\.$/);
});

test('nothing is shown without a cap or with unusable figures', () => {
	for (const value of [null, undefined, {}, { enabled: false }, spend(0, 0, 0), spend(2, -1, 3), spend(2, 'x', 1), { enabled: true, cap_usd: 2 }]) {
		assert.equal(spendView(value), null);
	}
});

test('credit usage feeds the store, and a response without spend clears it', () => {
	publishSpend({ balance: 5, provider_spend: spend(2, 0.5, 1.5) });
	assert.equal(get(spendStore).spent_usd, 0.5);
	publishSpend({ balance: 5 });
	assert.equal(get(spendStore), null);
	publishSpend(null);
	assert.equal(get(spendStore), null);
});

test('the client passes provider spend through unchanged', async () => {
	const originalFetch = globalThis.fetch;
	try {
		globalThis.fetch = async () => ({
			ok: true, status: 200,
			json: async () => ({ balance: 49, usage_history: [], provider_spend: spend(2, 0.37, 1.63) })
		});
		const usage = await new ApiClient().getCreditUsage();
		assert.deepEqual(usage.provider_spend, spend(2, 0.37, 1.63));
		assert.equal(usage.remaining_credits, 49);
	} finally { globalThis.fetch = originalFetch; }
});

test('a refused paid call because of the cap reads as a spend cap error, not a generic failure', () => {
	const shown = formatApiError({ code: 'SPEND_CAP_REACHED', status: 429, message: 'The personal spend cap is reached.' });
	assert.equal(shown.title, 'Spend cap reached');
	assert.equal(shown.message, 'The personal spend cap is reached.');
	assert.equal(shown.type, 'quota_exceeded');
});
