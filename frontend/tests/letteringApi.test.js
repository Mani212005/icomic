import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { ApiClient } from '../src/lib/api/client.js';
import { MockApiClient } from '../src/lib/api/mockClient.js';

const fixture = JSON.parse(readFileSync(new URL('./fixtures/lettering.json', import.meta.url)));

test('lettering client uses isolated read/placement/text endpoints and encoded IDs', async () => {
	const client = new ApiClient();
	const requests = [];
	client.request = async (path, options) => { requests.push({ path, options }); return { lettering_mode: 'overlay', lettering: fixture }; };
	assert.equal((await client.getLettering('page /')).lettering, fixture);
	await client.patchLetteringOverride('page /', 'utterance /', { center: { x: .3, y: .2 } });
	await client.patchLetteringText('page /', 'utterance /', 'Hello');
	assert.deepEqual(requests.map((r) => r.path), [
		'/api/v1/pages/page%20%2F/lettering',
		'/api/v1/pages/page%20%2F/lettering/utterances/utterance%20%2F/override',
		'/api/v1/pages/page%20%2F/lettering/utterances/utterance%20%2F/text'
	]);
	assert.deepEqual(JSON.parse(requests[1].options.body), { override: { center: { x: .3, y: .2 } } });
	assert.deepEqual(JSON.parse(requests[2].options.body), { text: 'Hello' });
	assert.equal(requests[2].options.method, 'PATCH');
});

test('mock samples stay baked and explicit overlays return replacement layouts without changing image state', async () => {
	const page = { id: fixture.input.page_id, chapter_id: 'chap-1', lettering_mode: 'overlay', draft_image_url: 'fake://text-free', draft_approved_at: 'approved', updated_at: 'unchanged' };
	const client = new MockApiClient({ letteringFixtures: [{ page, lettering: fixture }] });
	assert.deepEqual(await client.getLettering('page-1'), { lettering_mode: 'baked' });
	await assert.rejects(client.patchLetteringOverride('page-1', 'anything', {}), /Baked/);
	const id = fixture.input.panels[0].utterances[1].utterance_id;
	const response = await client.patchLetteringOverride(page.id, id, { center: { x: .4, y: .2 } });
	assert.deepEqual(response.lettering.overrides[id].center, { x: .4, y: .2 });
	await client.patchLetteringText(page.id, id, 'Hello!');
	assert.equal((await client.getLettering(page.id)).lettering.input.panels[0].utterances[1].text, 'Hello!');
	await client.patchLetteringOverride(page.id, id, {});
	assert.deepEqual((await client.getLettering(page.id)).lettering.overrides, {});
	for (const field of ['draft_image_url', 'draft_approved_at', 'updated_at']) assert.equal(client.pages.get(page.id)[field], page[field]);
});
