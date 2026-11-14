import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { STAGES } from '../../src/lib/api/types.js';

const PROJECT = { id: 'project-1', title: 'Tide', art_style: 'cyberpunk-noir', original_script: 'A hero finds a bird.', chapter_count: 2 };

beforeEach(() => localStorage.clear());
afterEach(cleanup);

function backend() {
	return {
		getCharacters: async () => [],
		getChapters: async () => [],
		getPanels: async () => [],
		getPages: async () => [],
		getColourPalette: async () => ({ palette: {} }),
		request: async () => ({}),
		getCreditUsage: async () => ({ remaining_credits: 5 }),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		updateProject: vi.fn(async () => PROJECT)
	};
}

const open = (apiClient, project = PROJECT) => render(ComicEditor, { project: { ...project }, apiClient, activeStage: STAGES.SCRIPT });
const pick = (view, file) => fireEvent.change(view.getByLabelText('Script file'), { target: { files: [file] } });

it('imports a Markdown file into the script exactly as written and saves it at once', async () => {
	const apiClient = backend();
	const view = open(apiClient, { ...PROJECT, original_script: '' });
	const markdown = '# The bird\r\n\r\nNia finds a *brass* bird.\r\n';
	await pick(view, new File([markdown], 'bird.md', { type: 'text/markdown' }));
	await view.findByText('Imported bird.md.');
	expect(view.getByLabelText('Script').value).toBe(markdown);
	await vi.waitFor(() => expect(apiClient.updateProject).toHaveBeenCalledTimes(1));
	expect(apiClient.updateProject.mock.calls[0][1].original_script).toBe(markdown);
	expect(view.getByText('Words:').textContent).toContain('8');
});

it('asks before replacing a script that already has text, and keeps it when declined', async () => {
	const apiClient = backend();
	const view = open(apiClient);
	await pick(view, new File(['A different story.'], 'other.txt', { type: 'text/plain' }));
	const confirm = await view.findByRole('alertdialog', { name: 'Confirm replace' });
	expect(confirm.textContent).toContain('other.txt');
	expect(view.getByLabelText('Script').value).toBe(PROJECT.original_script);
	expect(apiClient.updateProject).not.toHaveBeenCalled();

	await fireEvent.click(view.getByRole('button', { name: 'Keep' }));
	expect(view.queryByRole('alertdialog')).toBeNull();
	expect(view.getByLabelText('Script').value).toBe(PROJECT.original_script);

	await pick(view, new File(['A different story.'], 'other.txt', { type: 'text/plain' }));
	await fireEvent.click(await view.findByRole('button', { name: 'Replace' }));
	expect(view.getByLabelText('Script').value).toBe('A different story.');
	await vi.waitFor(() => expect(apiClient.updateProject.mock.calls.at(-1)[1].original_script).toBe('A different story.'));
});

it('refuses files it cannot use, says why, and leaves the script alone', async () => {
	const apiClient = backend();
	const view = open(apiClient);
	await pick(view, new File(['%PDF-1.4'], 'story.pdf', { type: 'application/pdf' }));
	expect((await view.findByRole('alert')).textContent).toContain('PDF files are not imported');

	await pick(view, new File([new Uint8Array(512 * 1024 + 1)], 'big.txt', { type: 'text/plain' }));
	await vi.waitFor(() => expect(view.getByRole('alert').textContent).toContain('513 KB. A script can be at most 512 KB'));

	await pick(view, new File(['  \n'], 'empty.md'));
	await vi.waitFor(() => expect(view.getByRole('alert').textContent).toContain('empty'));

	expect(view.getByLabelText('Script').value).toBe(PROJECT.original_script);
	expect(apiClient.updateProject).not.toHaveBeenCalled();
});

it('offers only text files in the file picker', async () => {
	const view = open(backend());
	expect(view.getByLabelText('Script file').getAttribute('accept')).toBe('.txt,.md,.markdown,text/plain,text/markdown');
});
