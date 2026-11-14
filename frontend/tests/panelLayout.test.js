import test from 'node:test';
import assert from 'node:assert/strict';
import { moveTarget, pageOfPanel } from '../src/lib/editor/panelLayout.js';
import { MockApiClient } from '../src/lib/api/mockClient.js';

const pages = [
	{ id: 'pg2', page_number: 2, panel_ids: ['c', 'd'] },
	{ id: 'pg1', page_number: 1, panel_ids: ['a', 'b'] },
	{ id: 'pg3', page_number: 3, panel_ids: ['e'] }
];

test('a panel steps within its page, then over the page boundary, in reading order', () => {
	assert.equal(pageOfPanel(pages, 'c').id, 'pg2');
	assert.equal(pageOfPanel(pages, 'zzz'), null);
	assert.deepEqual(moveTarget(pages, 'b', 'up'), { pageId: 'pg1', position: 0 });
	assert.deepEqual(moveTarget(pages, 'a', 'down'), { pageId: 'pg1', position: 1 });
	assert.deepEqual(moveTarget(pages, 'b', 'down'), { pageId: 'pg2', position: 0 }, 'down from the last panel enters the next page first');
	assert.deepEqual(moveTarget(pages, 'c', 'up'), { pageId: 'pg1', position: 2 }, 'up from the first panel joins the end of the previous page');
});

test('there is no step past the first or last panel, or one that would empty its page', () => {
	assert.equal(moveTarget(pages, 'a', 'up'), null);
	assert.equal(moveTarget(pages, 'e', 'down'), null);
	assert.equal(moveTarget(pages, 'e', 'up'), null, 'the only panel of a page cannot leave it');
	assert.equal(moveTarget(pages, 'zzz', 'up'), null);
});

// The demo mirrors the server: five panels per page, no empty page, only changed pages stale, numbers in order.
async function demo() {
	const api = new MockApiClient({ autoCompleteJobs: false });
	const [project] = await api.listProjects();
	const [chapter] = await api.getChapters(project.id);
	return { api, chapter };
}

test('demo add places a panel, stales only that page and numbers panels in reading order', async () => {
	const { api, chapter } = await demo();
	const [page] = await api.getPages(chapter.id);
	await assert.rejects(api.addPanel(chapter.id, { page_id: page.id, scene_description: ' ' }), /scene description/);
	const { panel } = await api.addPanel(chapter.id, { page_id: page.id, position: 0, scene_description: 'A gull' });
	const stored = await api.getPages(chapter.id);
	assert.equal(stored[0].panel_ids[0], panel.id);
	assert.equal(stored[0].draft_approved_at, null);
	const numbers = (await api.getPanels(chapter.id)).sort((a, b) => a.panel_number - b.panel_number);
	assert.deepEqual(numbers.map((p) => p.panel_number), [1, 2, 3, 4, 5]);
	assert.equal(numbers[0].id, panel.id);
	await assert.rejects(api.addPanel(chapter.id, { page_id: page.id, scene_description: 'Sixth' }), /at most 5/);
	await api.addPanel(chapter.id, { scene_description: 'Elsewhere' });
	assert.equal((await api.getPages(chapter.id)).length, 2);
});

test('demo move and delete keep pages non-empty and renumbered', async () => {
	const { api, chapter } = await demo();
	const [page] = await api.getPages(chapter.id);
	const [first, second] = page.panel_ids;
	await api.movePanel(chapter.id, second, page.id, 0);
	assert.deepEqual((await api.getPages(chapter.id))[0].panel_ids.slice(0, 2), [second, first]);
	const { panel } = await api.addPanel(chapter.id, { scene_description: 'Alone' });
	await assert.rejects(api.movePanel(chapter.id, panel.id, page.id, 0), /left without panels/, 'a move may not empty its page');
	await assert.rejects(api.movePanel(chapter.id, first, 'nope', 0), /not part of the chapter/);
	const removal = await api.deletePanel(chapter.id, panel.id);
	assert.equal(removal.removed_page_ids.length, 1, 'the emptied page goes with its last panel');
	assert.equal((await api.getPages(chapter.id)).length, 1);
	await assert.rejects(api.deletePanel(chapter.id, panel.id), /not found/);
	// The sample page has art, so its last panel stays.
	const [sample] = await api.getPages(chapter.id);
	for (const id of [...sample.panel_ids].slice(1)) await api.deletePanel(chapter.id, id);
	await assert.rejects(api.deletePanel(chapter.id, sample.panel_ids[0]), /generated art/);
	assert.equal((await api.getPages(chapter.id)).length, 1);
});
