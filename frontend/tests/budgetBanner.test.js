import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { ApiClient } from '../src/lib/api/client.js';
import { formatApiError } from '../src/lib/api/types.js';

const source = await readFile(new URL('../src/lib/editor/ErrorBanner.svelte', import.meta.url), 'utf8');
function compileSSR(source) {
	return compile(source, { generate: 'ssr' }).js.code
		.replaceAll('"svelte/internal"', JSON.stringify(import.meta.resolve('svelte/internal')));
}
function moduleURL(code) { return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`; }
const iconSource = await readFile(new URL('../src/lib/design/Icon.svelte', import.meta.url), 'utf8');
const iconModule = moduleURL(compileSSR(iconSource)
	.replace("'./icons.js'", JSON.stringify(new URL('../src/lib/design/icons.js', import.meta.url).href)));
const compiled = compileSSR(source).replace("'$lib/design/Icon.svelte'", JSON.stringify(iconModule));
const { default: Banner } = await import(moduleURL(compiled));

for (const [code, title, message] of [
	['book_budget_reached', 'Book budget reached', 'Book image budget used.'],
	['daily_image_limit_reached', 'Daily limit reached', 'Try again tomorrow.']
]) {
	test(`HTTP ${code} renders the budget message in the editor banner`, async () => {
		const originalFetch = globalThis.fetch;
		try {
			globalThis.fetch = async () => ({ ok: false, status: 429,
				json: async () => ({ error: 'quota detail from backend', code }) });
			let error;
			try { await new ApiClient().generateCharacterReference('hero'); } catch (err) { error = err; }
			assert.equal(error.code, code);
			const { html } = Banner.render({ error: formatApiError(error) });
			assert.match(html, /role="alert"/);
			assert.ok(html.includes(title));
			assert.ok(html.includes(message));
			assert.ok(!html.includes('quota detail from backend'));
			assert.ok(!html.includes('Next:'));
		} finally { globalThis.fetch = originalFetch; }
	});
}
