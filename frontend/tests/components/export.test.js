import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ExportView from '../../src/lib/export/ExportView.svelte';
import fixture from '../fixtures/lettering.json';

afterEach(cleanup);

async function choose(view, label, optionValue) {
	const select = view.getByLabelText(label);
	const chosen = [...select.options].find((option) => option.value === optionValue);
	[...select.options].forEach((option) => {
		option.selected = option === chosen;
	});
	// happy-dom does not implement `:checked` for options, which Svelte's select binding reads.
	const lookup = select.querySelector.bind(select);
	select.querySelector = (selector) => (selector === ':checked' ? chosen : lookup(selector));
	await fireEvent.change(select);
}

it('shows page-specific lettering warnings returned with a review PDF', async () => {
	const apiClient = {
		exportProject: vi.fn().mockResolvedValue({
			export: { id: 'review' },
			warnings: [{ page_number: 2, code: 'stale_lettering', message: 'Lettering is stale. Recompute and review before publishing.' }]
		}),
		saveExport: vi.fn().mockResolvedValue()
	};
	const view = render(ExportView, { projectId: 'project', apiClient });
	await fireEvent.click(view.getByRole('button', { name: 'Export PDF' }));
	const alert = await view.findByRole('alert');
	expect(alert.textContent).toContain('Page 2: Lettering is stale.');
	// The file is fetched with the client's credentials, never opened as a bare link.
	expect(view.queryByRole('link', { name: 'Download PDF' })).toBeNull();
	await fireEvent.click(view.getByRole('button', { name: 'Download PDF' }));
	await vi.waitFor(() => expect(apiClient.saveExport).toHaveBeenCalledWith('review', 'pdf'));
});

it.each([
	['a4', 'a4'],
	['us_letter', 'us_letter'],
	['custom', 'custom']
])('sends the selected %s page size with the PDF export request', async (option, expected) => {
	const apiClient = {
		exportProject: vi.fn().mockResolvedValue({ export: { id: 'sized' } }),
		saveExport: vi.fn()
	};
	const view = render(ExportView, { projectId: 'project', apiClient });
	await choose(view, 'Size', option);
	await fireEvent.click(view.getByRole('button', { name: 'Export PDF' }));
	expect(apiClient.exportProject).toHaveBeenCalledWith('project', { format: 'pdf', page_size: expected });
});

it('names the custom size by the dimensions the PDF is built to', () => {
	const view = render(ExportView, { projectId: 'project', apiClient: {} });
	expect(view.getByRole('option', { name: 'Custom (6.625 × 10.25 in)' })).toBeTruthy();
});

it('offers PNG pages as a ZIP at their original size', async () => {
	const apiClient = {
		exportProject: vi.fn().mockResolvedValue({ export: { id: 'zip' } }),
		saveExport: vi.fn().mockResolvedValue()
	};
	const view = render(ExportView, { projectId: 'project', apiClient });
	await choose(view, 'Format', 'png');
	expect(view.getByLabelText('Size').disabled).toBe(true);
	expect(view.getByRole('option', { name: 'Original size' })).toBeTruthy();
	await fireEvent.click(view.getByRole('button', { name: 'Export PNG' }));
	expect(apiClient.exportProject).toHaveBeenCalledWith('project', { format: 'png', page_size: 'a4' });
	await fireEvent.click(await view.findByRole('button', { name: 'Download ZIP' }));
	await vi.waitFor(() => expect(apiClient.saveExport).toHaveBeenCalledWith('zip', 'zip'));
});

it('shows why an export was refused and does not offer a download', async () => {
	const refusal = Object.assign(new Error('This comic is not ready to export: Page 2 has no approved, current artwork.'), {
		status: 409,
		code: 'EXPORT_NOT_READY'
	});
	const apiClient = { exportProject: vi.fn().mockRejectedValue(refusal), saveExport: vi.fn() };
	const view = render(ExportView, { projectId: 'project', apiClient });
	await fireEvent.click(view.getByRole('button', { name: 'Export PDF' }));
	const alert = await view.findByRole('alert');
	expect(alert.textContent).toContain('Export not ready');
	expect(alert.textContent).toContain('Page 2 has no approved, current artwork.');
	expect(view.queryByRole('button', { name: /Download/ })).toBeNull();
});

it('does not claim signature-verified provenance', () => {
	const view = render(ExportView, { projectId: 'project', apiClient: {} });
	expect(view.container.textContent).not.toMatch(/C2PA included/);
	expect(view.container.textContent).toContain('C2PA unverified');
});

describe('real artwork preview of the pages that will be exported', () => {
	const approved = '2026-11-06T00:00:10Z';
	const painted = (overrides = {}) => ({
		id: 'overlay-fixture',
		lettering_mode: 'overlay',
		updated_at: '2026-11-06T00:00:00Z',
		painted_page_image_url: '/media/painted.png',
		painted_page_approved_at: approved,
		...overrides
	});
	const api = (extra = {}) => ({
		getLettering: vi.fn().mockResolvedValue({ lettering_mode: 'overlay', lettering: fixture }),
		exportProject: vi.fn(),
		...extra
	});

	it('shows the approved artwork with the stored lettering drawn over it, not status tiles', async () => {
		const apiClient = api();
		const view = render(ExportView, { projectId: 'project', apiClient, pages: [painted()] });
		const image = view.getByAltText('Page 1');
		expect(image.getAttribute('src')).toBe('/media/painted.png');
		await vi.waitFor(() => expect(view.container.querySelector('.lettering-layer')).toBeTruthy());
		expect(apiClient.getLettering).toHaveBeenCalledWith('overlay-fixture');
		const layer = view.container.querySelector('.export-art .lettering-layer');
		expect(layer.querySelectorAll('[data-utterance]')).toHaveLength(fixture.layout.bubbles.length);
		expect(view.queryByRole('img', { name: /Review|did not load/ })).toBeNull();
		expect(view.container.textContent).not.toMatch(/Draft|Lines|Coloured/);
	});

	it('previews the newest current approval and ignores one an edit made stale', () => {
		const pages = [
			painted({
				id: 'a',
				lettering_mode: 'baked',
				line_art_image_url: '/media/lines.png',
				line_art_approved_at: '2026-11-06T00:00:05Z'
			}),
			painted({ id: 'b', lettering_mode: 'baked', updated_at: '2026-11-06T00:00:20Z' })
		];
		const view = render(ExportView, { projectId: 'project', apiClient: api(), pages });
		// The painted page is newer than the line art on page 1, and stale on page 2.
		expect(view.getByAltText('Page 1').getAttribute('src')).toBe('/media/painted.png');
		expect(view.queryByAltText('Page 2')).toBeNull();
		expect(view.getByRole('img', { name: 'No approved artwork' })).toBeTruthy();
	});

	it('draws no lettering for baked pages or pages whose lettering is part of the line art', async () => {
		const apiClient = api();
		const pages = [
			painted({ id: 'baked', lettering_mode: 'baked' }),
			painted({ id: 'ink', line_art_image_url: '/media/lines.png', line_art_approved_at: '2026-11-06T00:00:05Z' })
		];
		const view = render(ExportView, { projectId: 'project', apiClient, pages });
		await Promise.resolve();
		expect(apiClient.getLettering).not.toHaveBeenCalled();
		expect(view.container.querySelector('.lettering-layer')).toBeNull();
	});

	it('says so when the stored lettering cannot be loaded, and flags review issues', async () => {
		const failing = api({ getLettering: vi.fn().mockRejectedValue(new Error('offline')) });
		const view = render(ExportView, { projectId: 'project', apiClient: failing, pages: [painted()] });
		await view.findByRole('img', { name: 'Lettering did not load' });

		const flagged = structuredClone(fixture);
		flagged.layout.issues = [{ panel_id: 'p', utterance_id: 'u', flag: 'overflow' }];
		const second = render(ExportView, {
			projectId: 'project',
			apiClient: api({ getLettering: vi.fn().mockResolvedValue({ lettering: flagged }) }),
			pages: [painted()]
		});
		await second.findByRole('img', { name: 'Review: overflow' });
	});

	it('marks the page an export flagged', async () => {
		const apiClient = api({
			exportProject: vi.fn().mockResolvedValue({
				export: { id: 'review' },
				warnings: [{ page_number: 1, code: 'stale_lettering', message: 'Lettering is stale.' }]
			})
		});
		const view = render(ExportView, { projectId: 'project', apiClient, pages: [painted(), painted({ id: 'two' })] });
		await fireEvent.click(view.getByRole('button', { name: 'Export PDF' }));
		await view.findByRole('img', { name: 'Flagged in the last export' });
		expect(view.getAllByRole('img', { name: 'Flagged in the last export' })).toHaveLength(1);
	});
});
