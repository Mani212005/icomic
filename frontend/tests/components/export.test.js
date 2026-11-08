import { afterEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ExportView from '../../src/lib/export/ExportView.svelte';

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
