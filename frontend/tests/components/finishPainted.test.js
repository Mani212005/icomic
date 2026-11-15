import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import Editor from '../../src/lib/editor/ComicEditor.svelte';
import { MockApiClient } from '../../src/lib/api/mockClient.js';
import { stageList } from '../../src/lib/editor/stages.js';
import { exportArtworkUrl } from '../../src/lib/api/types.js';
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

// A page that already went through the retired Ink route: approved line art and an approved
// coloured image, no painted page. Projects like this must keep opening, displaying and exporting.
const APPROVED = '2026-11-06T00:00:05Z';
const inkColouredPage = {
	id: 'ink-coloured',
	chapter_id: 'chap-1',
	page_number: 7,
	lettering_mode: 'baked',
	draft_image_url: '/mock/draft.svg',
	line_art_image_url: '/mock/line-art.svg',
	line_art_approved_at: APPROVED,
	colour_image_url: '/mock/coloured.png',
	colour_approved_at: '2026-11-06T00:00:10Z',
	updated_at: '2026-11-06T00:00:00Z'
};

async function finishStage(options = {}) {
	const api = new MockApiClient(options);
	const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'line_art' });
	await waitFor(() => expect(view.container.querySelector('.page-preview-card')).toBeTruthy());
	return { api, view };
}

describe('Finish stage: the painted page is the only final route', () => {
	it('has no Ink step, no variant choice and no Ink copy', async () => {
		expect(stageList.map((stage) => stage.label)).toEqual([
			'1. Script', '2. Characters', '3. Outline', '4. Panels', '5. Drafts', '6. Finish', '7. Export'
		]);
		const { view } = await finishStage({ letteringFixtures: [{ page: overlayPage, lettering: fixture }] });
		expect(view.queryByLabelText('Variant')).toBeNull();
		expect(view.container.textContent).not.toMatch(/\bInk\b|Colour|overlay-lettered/);
		expect(view.getByLabelText('Finish')).toBeTruthy();
		// Every page, overlay-lettered or not, can be regenerated and approved on the same route.
		for (const card of view.container.querySelectorAll('.page-preview-card')) {
			expect(card.querySelector('button[aria-label="Regenerate"]').disabled).toBe(false);
			expect(card.querySelector('.approve-btn, .reapprove-btn, .status-approved')).toBeTruthy();
			expect(card.querySelector('.approve-btn, .reapprove-btn')?.disabled ?? false).toBe(false);
		}
	});

	it('regenerates and approves only through the painted finish', async () => {
		const { api, view } = await finishStage({ letteringFixtures: [{ page: overlayPage, lettering: fixture }] });
		const generate = vi.spyOn(api, 'generateLineArt');
		const approve = vi.spyOn(api, 'approveLineArt');
		const request = (api.request = vi.fn(async () => ({})));
		const overlay = [...view.container.querySelectorAll('.page-preview-card')].find((card) => card.textContent.includes('#9'));
		await fireEvent.click(overlay.querySelector('button[aria-label="Regenerate"]'));
		await waitFor(() => expect(generate).toHaveBeenCalledWith(overlayPage.id, expect.any(Object)));
		await fireEvent.click(overlay.querySelector('.approve-btn'));
		await waitFor(() => expect(approve).toHaveBeenCalledWith(overlayPage.id));
		expect(request.mock.calls.some(([path]) => String(path).includes('variant=ink'))).toBe(false);
	});

	it('shows a page finished with Ink before the route was retired, and lets it go on to Export', async () => {
		const api = new MockApiClient();
		const page = { ...inkColouredPage };
		for (const [id] of api.pages) api.pages.delete(id);
		api.pages.set(page.id, page);
		const view = render(Editor, { apiClient: api, project: (await api.listProjects())[0], activeStage: 'line_art' });
		const card = await waitFor(() => {
			const found = view.container.querySelector('.page-preview-card');
			expect(found).toBeTruthy();
			return found;
		});
		expect(card.querySelector('img').getAttribute('src')).toBe('/mock/line-art.svg');
		expect(card.querySelector('.status-approved')).toBeTruthy();
		const next = view.getByRole('button', { name: /Export/ });
		expect(next.disabled).toBe(false);
		await fireEvent.click(next);
		await view.findByRole('button', { name: 'Export PDF' });
		// The export still picks the newest approved artwork: the coloured page, as before.
		expect(exportArtworkUrl(page)).toBe('/mock/coloured.png');
	});
});
