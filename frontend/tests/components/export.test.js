import { afterEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ExportView from '../../src/lib/export/ExportView.svelte';

afterEach(cleanup);
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
