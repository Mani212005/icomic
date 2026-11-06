import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import Layer from '../../src/lib/lettering/LetteringLayer.svelte';
import Preview from '../../src/lib/lettering/LetteringPreview.svelte';
import Editor from '../../src/lib/editor/ComicEditor.svelte';
import { LetteringSession } from '../../src/lib/lettering/session.js';
import { MockApiClient } from '../../src/lib/api/mockClient.js';
import { movedOverride, previewOverride } from '../../src/lib/lettering/geometry.js';
import fixture from '../fixtures/lettering.json';
import devanagari from '../../../ai/tests/fixtures/lettering/unicode-devanagari.golden.json';

const speech = 'overlay-panel-dialogue-0';
const page = { id: 'overlay-fixture', chapter_id: 'chap-1', lettering_mode: 'overlay', draft_image_url: '/mock/draft.svg', draft_approved_at: '2026-11-06T00:00:01Z', updated_at: '2026-11-06T00:00:00Z' };
function setup() {
	const api = new MockApiClient({ letteringFixtures: [{ page, lettering: fixture }] });
	const session = new LetteringSession(api, page.id);
	const view = render(Preview, { session, imageUrl: page.draft_image_url, imageAlt: 'Page' });
	return { ...view, api, session };
}
async function ready(view) {
	const body = await view.findByRole('button', { name: 'Nia: We found it!' });
	view.container.querySelector('.lettering-frame').getBoundingClientRect = () => ({ x: 0, y: 0, width: 500, height: 750 });
	body.setPointerCapture = vi.fn();
	return body;
}
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('serialized vector rendering', () => {
	it('snapshots exact paths, glyph origins, inside strokes and a seamless tail', () => {
		const { container } = render(Layer, { layout: fixture.layout, idPrefix: 'fixture' });
		expect(container.innerHTML).toMatchSnapshot();
		expect(container.querySelector('svg').getAttribute('preserveAspectRatio')).toBe('none');
		expect(container.querySelectorAll('text')).toHaveLength(0);
		expect(container.querySelectorAll('[data-line] path')).toHaveLength(fixture.layout.bubbles.flatMap((b) => b.lines.flatMap((l) => l.glyphs)).length);
	});
	it('loads complex-script glyph IDs without browser reshaping', async () => {
		const { container } = render(Layer, { layout: devanagari });
		await waitFor(() => expect(container.querySelectorAll('[data-line] path').length).toBe(devanagari.bubbles.flatMap((b) => b.lines.flatMap((l) => l.glyphs)).length));
	});
});

describe('placement controls', () => {
	it('drags a bubble with a captured touch pointer, commits panel-local coordinates, and leaves art approval unchanged', async () => {
		const view = setup(), body = await ready(view);
		const before = structuredClone(view.api.pages.get(page.id));
		await fireEvent.pointerDown(body, { button: 0, pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 100 });
		await fireEvent.pointerMove(body, { pointerId: 1, clientX: 250, clientY: 175 });
		expect(view.session.value.lettering.overrides).toEqual({});
		await fireEvent.pointerUp(body, { pointerId: 1 });
		await waitFor(() => expect(view.session.value.busy).toBe(false));
		expect(view.session.value.lettering.overrides[speech]).toEqual(movedOverride(fixture, speech, { x: .1, y: .1 }));
		expect(view.api.pages.get(page.id).draft_image_url).toBe(before.draft_image_url);
		expect(view.api.pages.get(page.id).draft_approved_at).toBe(before.draft_approved_at);
		expect(view.api.pages.get(page.id).updated_at).toBe(before.updated_at);
	});
	it('repoints the tail separately from the body', async () => {
		const view = setup(), body = await ready(view);
		await fireEvent.focus(body);
		const tail = view.getByRole('button', { name: 'Tail' });
		tail.setPointerCapture = vi.fn();
		await fireEvent.pointerDown(tail, { button: 0, pointerId: 2, clientX: 200, clientY: 100 });
		await fireEvent.pointerMove(tail, { pointerId: 2, clientX: 230, clientY: 145 });
		await fireEvent.pointerUp(tail, { pointerId: 2 });
		await waitFor(() => expect(view.session.value.busy).toBe(false));
		expect(view.session.value.lettering.overrides[speech]).toEqual(movedOverride(fixture, speech, { x: .06, y: .06 }, true));
		expect(view.session.value.lettering.layout.bubbles[1].shape).toEqual(fixture.layout.bubbles[1].shape);
	});
	it('debounces keyboard nudges, supports larger steps, and undoes/redoes/reset placements', async () => {
		const view = setup(), body = await ready(view);
		await fireEvent.keyDown(body, { key: 'ArrowRight' });
		await fireEvent.keyDown(body, { key: 'ArrowDown', shiftKey: true });
		await waitFor(() => expect(view.session.value.undo).toHaveLength(1));
		const changed = structuredClone(view.session.value.lettering.overrides);
		expect(changed[speech].center.x).toBeCloseTo(movedOverride(fixture, speech, { x: .001, y: .01 }).center.x);
		await fireEvent.click(view.getByRole('button', { name: 'Undo' }));
		await waitFor(() => expect(view.session.value.lettering.overrides).toEqual({}));
		await fireEvent.click(view.getByRole('button', { name: 'Redo' }));
		await waitFor(() => expect(view.session.value.lettering.overrides).toEqual(changed));
		await fireEvent.click(view.getByRole('button', { name: 'Reset' }));
		await waitFor(() => expect(view.session.value.lettering.overrides).toEqual({}));
	});
	it('resizes within limits and replaces provisional geometry with server geometry', async () => {
		const view = setup(), body = await ready(view);
		await fireEvent.focus(body);
		await fireEvent.input(view.getByRole('slider', { name: 'Size' }), { target: { value: '1.5' } });
		await waitFor(() => expect(view.session.value.undo).toHaveLength(1));
		expect(view.session.value.lettering.overrides[speech].size_scale).toBe(1.5);
		expect(view.session.value.lettering.layout.bubbles[1].lines[0].font_size).toBeCloseTo(fixture.layout.bubbles[1].lines[0].font_size * 1.5);
	});
	it('exposes server flags by accessible names, including flags without a bubble', async () => {
		const view = setup(); await ready(view);
		view.session.publish({ lettering: { ...fixture, layout: { ...fixture.layout, issues: ['overflow', 'collision', 'out_of_panel', 'unsupported_glyph'].map((flag) => ({ flag, panel_id: 'overlay-panel' })) } } });
		await tick();
		for (const name of ['overflow', 'collision', 'out of panel', 'unsupported glyph']) expect(view.getByRole('img', { name })).toBeTruthy();
	});
	it('rolls back rejected optimistic edits and announces the failure', async () => {
		const view = setup(), body = await ready(view);
		view.api.patchLetteringOverride = vi.fn().mockRejectedValue(new Error('409'));
		await fireEvent.keyDown(body, { key: 'ArrowRight' });
		await waitFor(() => expect(view.getByRole('alert').textContent).toContain('Save failed.'));
		expect(view.session.value.lettering).toEqual(fixture);
		expect(view.session.value.undo).toHaveLength(0);
	});
	it('cancels an unfinished drag on Escape and pointer cancellation', async () => {
		const view = setup(), body = await ready(view);
		await fireEvent.pointerDown(body, { button: 0, pointerId: 3, clientX: 200, clientY: 100 });
		await fireEvent.pointerMove(body, { pointerId: 3, clientX: 230, clientY: 145 });
		await fireEvent.keyDown(body, { key: 'Escape' });
		expect(view.session.value.lettering.overrides).toEqual({});
		expect(view.session.value.selected).toBeNull();
	});
	it('enters inline text on Enter, debounces live layout, and restores focus on Escape', async () => {
		const view = setup(), body = await ready(view);
		await fireEvent.keyDown(body, { key: 'Enter' });
		const input = view.getByRole('textbox', { name: 'Text' });
		expect(document.activeElement).toBe(input);
		await fireEvent.input(input, { target: { value: 'Hello!' } });
		await waitFor(() => expect(view.session.value.lettering.input.panels[0].utterances[1].text).toBe('Hello!'));
		expect(view.session.value.lettering.layout.bubbles[1].lines[0].text).toBe('Hello!');
		await fireEvent.keyDown(input, { key: 'Escape' });
		expect(view.queryByRole('textbox', { name: 'Text' })).toBeNull();
		expect(document.activeElement).toBe(body);
	});
});


describe('editor eligibility and session continuity', () => {
	it('shows no lettering or controls for baked sample pages and refreshes asynchronous cost labels', async () => {
		const api = new MockApiClient();
		const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'draft' });
		await view.findByAltText('Draft 1');
		await waitFor(() => expect(view.getByRole('button', { name: 'Regen (Unverified estimate: $0.01)' })).toBeTruthy());
		expect(view.container.querySelector('.lettering-layer')).toBeNull();
		expect(view.queryByRole('group', { name: 'Lettering' })).toBeNull();
	});
	it('retains placement history when the page preview remounts', async () => {
		const view = setup(), body = await ready(view);
		await fireEvent.keyDown(body, { key: 'ArrowRight' });
		await waitFor(() => expect(view.session.value.undo).toHaveLength(1));
		view.unmount();
		const remounted = render(Preview, { session: view.session, imageUrl: page.draft_image_url, imageAlt: 'Page' });
		await waitFor(() => expect(remounted.getByRole('button', { name: 'Undo' }).disabled).toBe(false));
	});
	it('flushes the latest inline text after unmount even if the prior request is still running', async () => {
		const view = setup(), body = await ready(view);
		const original = view.api.patchLetteringText.bind(view.api);
		let release;
		const gate = new Promise((resolve) => release = resolve);
		view.api.patchLetteringText = async (...args) => { await gate; return original(...args); };
		await fireEvent.keyDown(body, { key: 'Enter' });
		const input = view.getByRole('textbox', { name: 'Text' });
		await fireEvent.input(input, { target: { value: 'First' } });
		await waitFor(() => expect(view.session.value.busy).toBe(true));
		await fireEvent.input(input, { target: { value: 'Last' } });
		view.unmount();
		release();
		await view.session.pendingRequest;
		expect(view.session.value.lettering.input.panels[0].utterances[1].text).toBe('Last');
	});
});


it('replaces optimistic geometry with the authoritative server result', async () => {
	const view = setup(), body = await ready(view);
	const authoritative = structuredClone(fixture);
	authoritative.overrides[speech] = { size_scale: 1.5 };
	authoritative.layout.bubbles[1].shape.bounds = { x: .21, y: .18, width: .31, height: .22 };
	view.api.patchLetteringOverride = vi.fn().mockResolvedValue({ lettering: authoritative });
	await fireEvent.focus(body);
	await fireEvent.input(view.getByRole('slider', { name: 'Size' }), { target: { value: '1.5' } });
	await waitFor(() => expect(view.session.value.undo).toHaveLength(1));
	expect(view.session.value.lettering).toEqual(authoritative);
	expect(body.style.left).toBe('21%');
	expect(body.style.width).toBe('31%');
});

it('restores rejected inline text and announces the failure', async () => {
	const view = setup(), body = await ready(view);
	view.api.patchLetteringText = vi.fn().mockRejectedValue(new Error('409'));
	await fireEvent.keyDown(body, { key: 'Enter' });
	const input = view.getByRole('textbox', { name: 'Text' });
	await fireEvent.input(input, { target: { value: 'Rejected' } });
	await waitFor(() => expect(view.getByRole('alert').textContent).toContain('Save failed.'));
	await waitFor(() => expect(input.value).toBe('We found it!'));
});

it('refreshes source panels after inline lettering edits when returning to Panels', async () => {
	const api = new MockApiClient({ letteringFixtures: [{ page, lettering: fixture }] });
	const original = api.getPanels.bind(api);
	api.getPanels = async (chapterId) => {
		const state = api.pages.get(page.id).lettering;
		const text = state.input.panels[0].utterances[1].text;
		return [...await original(chapterId), { id: 'overlay-panel', chapter_id: chapterId, panel_number: 5, dialogue: text, narration: '', scene_description: '' }];
	};
	const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'draft' });
	const body = await view.findByRole('button', { name: 'Nia: We found it!' });
	await fireEvent.keyDown(body, { key: 'Enter' });
	const input = view.getByRole('textbox', { name: 'Text' });
	await fireEvent.input(input, { target: { value: 'Fresh lettering' } });
	await waitFor(() => expect(api.pages.get(page.id).lettering.input.panels[0].utterances[1].text).toBe('Fresh lettering'));
	await fireEvent.keyDown(input, { key: 'Escape' });
	await fireEvent.click(view.getByRole('button', { name: '4. Panels Complete' }));
	await waitFor(() => expect(view.getByText('"Fresh lettering"')).toBeTruthy());
});

it('refreshes a cached lettering session when source panels reload', async () => {
	const api = new MockApiClient({ letteringFixtures: [{ page, lettering: fixture }] });
	const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'draft' });
	await view.findByRole('button', { name: 'Nia: We found it!' });
	await api.patchLetteringText(page.id, speech, 'Updated source');
	await view.component.reloadProjectData();
	await waitFor(() => expect(view.getByRole('button', { name: 'Nia: Updated source' })).toBeTruthy());
});
