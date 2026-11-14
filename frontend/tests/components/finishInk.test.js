import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import Editor from '../../src/lib/editor/ComicEditor.svelte';
import { MockApiClient } from '../../src/lib/api/mockClient.js';
import fixture from '../fixtures/lettering.json';

afterEach(cleanup);

const overlayPage = {
	id: 'overlay-fixture',
	chapter_id: 'chap-1',
	page_number: 9,
	lettering_mode: 'overlay',
	draft_image_url: '/mock/draft.svg',
	painted_page_image_url: '/mock/painted.svg',
	updated_at: '2026-11-06T00:00:00Z'
};

async function finishStage({ onlyOverlay = false } = {}) {
	const api = new MockApiClient({ letteringFixtures: [{ page: overlayPage, lettering: fixture }] });
	if (onlyOverlay) {
		for (const [id, page] of api.pages) if (page.lettering_mode !== 'overlay') api.pages.delete(id);
	}
	const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'line_art' });
	await waitFor(() => expect(view.container.querySelector('.page-preview-card')).toBeTruthy());
	return { api, view };
}

async function selectInk(view) {
	const select = view.getByLabelText('Variant');
	const chosen = [...select.options].find((option) => option.value === 'ink');
	[...select.options].forEach((option) => (option.selected = option === chosen));
	// happy-dom does not implement `:checked` for options, which Svelte's select binding reads.
	const lookup = select.querySelector.bind(select);
	select.querySelector = (selector) => (selector === ':checked' ? chosen : lookup(selector));
	await fireEvent.change(select);
}

const NOTE = 'Ink is not available for overlay-lettered pages. Use Painted.';
const inkOption = (view) => [...view.getByLabelText('Variant').options].find((o) => o.value === 'ink');

describe('Finish stage and Ink for overlay-lettered pages', () => {
	it('offers Ink and shows no note while no page is overlay-lettered', async () => {
		const api = new MockApiClient();
		const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'line_art' });
		await waitFor(() => expect(view.container.querySelector('.page-preview-card')).toBeTruthy());
		expect(inkOption(view).disabled).toBe(false);
		await selectInk(view);
		expect(view.queryByText(NOTE)).toBeNull();
		expect(view.getByRole('button', { name: 'Regenerate' }).disabled).toBe(false);
	});

	it('disables Ink with the reason when every page is overlay-lettered', async () => {
		const { view } = await finishStage({ onlyOverlay: true });
		expect(inkOption(view).disabled).toBe(true);
		expect(inkOption(view).title).toBe(NOTE);
		expect(view.getByLabelText('Variant').value).toBe('painted');
		expect(view.getByRole('button', { name: 'Regenerate' }).disabled).toBe(false);
	});

	it('says Ink is unavailable and blocks its actions on overlay pages instead of failing with 409', async () => {
		const { api, view } = await finishStage();
		const generate = vi.spyOn(api, 'generateLineArt');
		const approve = vi.spyOn(api, 'approveLineArt');
		await selectInk(view);
		expect(await view.findByText(NOTE)).toBeTruthy();
		const cards = [...view.container.querySelectorAll('.page-preview-card')];
		const overlay = cards.find((card) => card.textContent.includes('#9'));
		const baked = cards.find((card) => !card.textContent.includes('#9'));
		const buttons = (card) => [...card.querySelectorAll('button')].filter((b) => /Regenerate|Approve/.test(b.getAttribute('aria-label') || b.textContent));
		expect(buttons(overlay).length).toBeGreaterThan(0);
		for (const button of buttons(overlay)) {
			expect(button.disabled).toBe(true);
			await fireEvent.click(button);
		}
		expect(generate).not.toHaveBeenCalled();
		expect(approve).not.toHaveBeenCalled();
		// Baked pages keep the Ink route that C1 leaves untouched.
		for (const button of buttons(baked)) expect(button.disabled).toBe(false);
	});
});
