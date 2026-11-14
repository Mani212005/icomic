import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { STAGES } from '../../src/lib/api/types.js';

const PROJECT = { id: 'project-1', title: 'Tide', original_script: 'A hero finds a bird.', chapter_count: 1 };
const CHAPTER = { id: 'chap-1', chapter_number: 1, title: 'The bird', summary: 'A bird', status: 'approved', updated_at: '2026-11-02T10:00:00Z' };
const panel = (n, scene) => ({ id: `panel-${n}`, chapter_id: 'chap-1', panel_number: n, scene_description: scene, dialogue: '', narration: '', character_actions: '', camera_angle: '', background: '', image_prompt: '' });
const PANELS = [panel(1, 'First scene'), panel(2, 'Second scene'), panel(3, 'Third scene')];
const PAGES = [
	{ id: 'page-1', chapter_id: 'chap-1', page_number: 1, panel_ids: ['panel-1', 'panel-2'], updated_at: '2026-11-02T10:00:00Z' },
	{ id: 'page-2', chapter_id: 'chap-1', page_number: 2, panel_ids: ['panel-3'], updated_at: '2026-11-02T10:00:00Z' }
];

beforeEach(() => localStorage.clear());
afterEach(cleanup);

// happy-dom does not implement `:checked` for options, which Svelte's select binding reads.
async function choose(select, value) {
	const chosen = [...select.options].find((option) => option.value === value);
	[...select.options].forEach((option) => (option.selected = option === chosen));
	const lookup = select.querySelector.bind(select);
	select.querySelector = (selector) => (selector === ':checked' ? chosen : lookup(selector));
	await fireEvent.change(select);
}

// Structure edits disable their controls while they run and reload, so a click waits for the control.
async function press(view, name) {
	const button = await vi.waitFor(() => {
		const found = view.getByRole('button', { name });
		expect(found.disabled).toBe(false);
		return found;
	});
	await fireEvent.click(button);
}

function backend(overrides = {}) {
	const calls = [];
	const apiClient = {
		getCharacters: async () => [],
		getChapters: async () => [CHAPTER],
		getPanels: async () => PANELS,
		getPages: async () => PAGES,
		getColourPalette: async () => ({ palette: {} }),
		request: async () => ({}),
		getCreditUsage: async () => ({ remaining_credits: 5 }),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		movePanel: vi.fn(async (...args) => (calls.push(['move', ...args]), {})),
		deletePanel: vi.fn(async (...args) => (calls.push(['delete', ...args]), {})),
		addPanel: vi.fn(async (...args) => (calls.push(['add', ...args]), {})),
		...overrides
	};
	return { apiClient, calls };
}

// Panels render before the pages load; the page chips mark the editor as ready for structure edits.
async function open(apiClient) {
	const view = render(ComicEditor, { project: { ...PROJECT }, apiClient, activeStage: STAGES.PANELS });
	await vi.waitFor(() => expect(view.getAllByText(/^Page [12]$/).length).toBe(3));
	return view;
}

it('shows the page each panel sits on and offers steps only where one is possible', async () => {
	const view = await open(backend().apiClient);
	expect(view.getAllByText(/^Page [12]$/).map((chip) => chip.textContent)).toEqual(['Page 1', 'Page 1', 'Page 2']);
	expect(view.getByRole('button', { name: 'Move panel 1 up' }).disabled).toBe(true);
	expect(view.getByRole('button', { name: 'Move panel 1 down' }).disabled).toBe(false);
	expect(view.getByRole('button', { name: 'Move panel 3 up' }).disabled).toBe(true);
	expect(view.getByRole('button', { name: 'Move panel 3 down' }).disabled).toBe(true);
});

it('moves a panel one step in reading order, across a page boundary when it is at the edge', async () => {
	const { apiClient, calls } = backend();
	const view = await open(apiClient);
	await fireEvent.click(await view.findByRole('button', { name: 'Move panel 1 down' }));
	await vi.waitFor(() => expect(calls).toEqual([['move', 'chap-1', 'panel-1', 'page-1', 1]]));
	await press(view, 'Move panel 2 down');
	await vi.waitFor(() => expect(calls[1]).toEqual(['move', 'chap-1', 'panel-2', 'page-2', 0]));
});

it('asks before deleting a panel and deletes only on confirmation', async () => {
	const { apiClient, calls } = backend();
	const view = await open(apiClient);
	await fireEvent.click(await view.findByRole('button', { name: 'Delete panel 2' }));
	expect(view.getByRole('alertdialog', { name: 'Confirm delete' }).textContent).toContain('Its pages need approval again');
	expect(calls).toEqual([]);
	await fireEvent.click(view.getByRole('button', { name: 'Keep' }));
	expect(view.queryByRole('alertdialog')).toBeNull();
	expect(calls).toEqual([]);

	await fireEvent.click(view.getByRole('button', { name: 'Delete panel 2' }));
	await fireEvent.click(view.getByRole('button', { name: 'Delete' }));
	await vi.waitFor(() => expect(calls).toEqual([['delete', 'chap-1', 'panel-2']]));
	await vi.waitFor(() => expect(view.queryByRole('alertdialog')).toBeNull());
});

it('adds a panel to a chosen page and needs a scene first', async () => {
	const { apiClient, calls } = backend();
	const view = await open(apiClient);
	await fireEvent.click(await view.findByRole('button', { name: /Add panel/ }));
	const add = view.getByRole('button', { name: 'Add panel' });
	expect(add.disabled).toBe(true);
	await fireEvent.input(view.getByLabelText(/^Scene:/), { target: { value: '  A gull lands  ' } });
	await fireEvent.input(view.getByLabelText(/^Dialogue:/), { target: { value: 'Gull: Hello' } });
	expect(view.getByLabelText(/^Page:/).value).toBe('page-2');
	await fireEvent.click(add);
	await vi.waitFor(() => expect(calls).toEqual([['add', 'chap-1', { scene_description: 'A gull lands', dialogue: 'Gull: Hello', page_id: 'page-2' }]]));
	await vi.waitFor(() => expect(view.queryByLabelText(/^Page:/)).toBeNull());
});

it('adds a panel on a new page by leaving out the page', async () => {
	const { apiClient, calls } = backend();
	const view = await open(apiClient);
	await fireEvent.click(await view.findByRole('button', { name: /Add panel/ }));
	await choose(view.getByLabelText(/^Page:/), 'new');
	await fireEvent.input(view.getByLabelText(/^Scene:/), { target: { value: 'The lighthouse' } });
	await fireEvent.click(view.getByRole('button', { name: 'Add panel' }));
	await vi.waitFor(() => expect(calls).toEqual([['add', 'chap-1', { scene_description: 'The lighthouse' }]]));
});

it('shows a refusal, keeps the form and the panel, and reloads what is stored', async () => {
	let loads = 0;
	const { apiClient } = backend({
		getPanels: async () => (loads++, PANELS),
		addPanel: vi.fn(async () => { throw Object.assign(new Error('a page holds at most 5 panels'), { status: 400 }); }),
		deletePanel: vi.fn(async () => { throw Object.assign(new Error('This is the last panel on a page that already has generated art.'), { status: 409 }); })
	});
	const view = await open(apiClient);
	await fireEvent.click(await view.findByRole('button', { name: 'Delete panel 3' }));
	const before = loads;
	await fireEvent.click(view.getByRole('button', { name: 'Delete' }));
	await view.findByText(/generated art/);
	expect(loads).toBeGreaterThan(before);
	expect(view.getByRole('alertdialog')).toBeTruthy();

	await press(view, 'Keep');
	await press(view, /Add panel/);
	await fireEvent.input(view.getByLabelText(/^Scene:/), { target: { value: 'One more' } });
	await fireEvent.click(view.getByRole('button', { name: 'Add panel' }));
	await view.findByText(/at most 5 panels/);
	expect(view.getByLabelText(/^Scene:/).value).toBe('One more');
});
