import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { STAGES } from '../../src/lib/api/types.js';

const PROJECT = { id: 'project-1', title: 'Tide', original_script: 'A hero.', chapter_count: 1 };

beforeEach(() => localStorage.clear());
afterEach(cleanup);

// A backend whose provider takes as long as the test lets it: the receipt returns at once, the job
// is polled, and the character only gains its image when the job is done.
function slowBackend() {
	const state = { image: '', status: 'running', posts: [], polls: 0 };
	const apiClient = {
		async getCharacters() {
			return [{ id: 'char-1', name: 'Hero', description: 'Keeper', reference_image_url: state.image }];
		},
		getChapters: async () => [],
		getCreditUsage: async () => ({ remaining_credits: 5 }),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		generateCharacterReference: vi.fn(async (id, options) => {
			state.posts.push(options.idempotencyKey);
			return { job: { id: 'job-1', status: 'pending' }, job_id: 'job-1', entity_type: 'character', entity_id: id };
		}),
		async getJob(jobId) {
			state.polls += 1;
			return { id: jobId, status: state.status };
		}
	};
	return { state, apiClient };
}

function renderEditor(apiClient) {
	return render(ComicEditor, { project: PROJECT, apiClient, activeStage: STAGES.CHARACTERS });
}

it('returns control at once, polls the job with progress, and shows the finished entity when it is done', async () => {
	const { state, apiClient } = slowBackend();
	const view = renderEditor(apiClient);
	const regenerate = await view.findByRole('button', { name: /Regen/ });
	await fireEvent.click(regenerate);

	// The receipt is in, the provider is not: the banner follows the job and the controls stay locked.
	await vi.waitFor(() => expect(view.getByRole('status').textContent).toContain('Generating'));
	expect(view.getByRole('button', { name: /Regen/ }).disabled).toBe(true);
	await fireEvent.click(view.getByRole('button', { name: /Regen/ }));
	expect(apiClient.generateCharacterReference).toHaveBeenCalledTimes(1);
	expect(state.polls).toBeGreaterThan(0);
	expect(view.queryByRole('img', { name: 'Hero' })).toBeNull();

	state.image = 'https://images.example/hero.png';
	state.status = 'done';
	const image = await view.findByRole('img', { name: 'Hero' }, { timeout: 3000 });
	expect(image.getAttribute('src')).toBe('https://images.example/hero.png');
	await vi.waitFor(() => expect(view.queryByRole('status')).toBeNull());
	expect(view.getByRole('button', { name: /Regen/ }).disabled).toBe(false);
	expect(apiClient.generateCharacterReference).toHaveBeenCalledTimes(1);
});

it('resumes a remembered job by ID after a reload and shows the final entity without a second submission', async () => {
	const { state, apiClient } = slowBackend();
	const first = renderEditor(apiClient);
	await fireEvent.click(await first.findByRole('button', { name: /Regen/ }));
	await vi.waitFor(() => expect(first.getByRole('status').textContent).toContain('Generating'));
	first.unmount(); // the tab is closed or reloaded while the provider is still working

	// The provider finished while nobody was looking.
	state.image = 'https://images.example/hero.png';
	state.status = 'done';
	const reloaded = renderEditor(apiClient);
	const image = await reloaded.findByRole('img', { name: 'Hero' }, { timeout: 3000 });
	expect(image.getAttribute('src')).toBe('https://images.example/hero.png');
	expect(apiClient.generateCharacterReference).toHaveBeenCalledTimes(1);
	await vi.waitFor(() => expect(reloaded.queryByRole('status')).toBeNull());
	expect(localStorage.getItem('icomic.activeJob.project-1')).toBeNull();
});

it('keeps the controls locked on reload while the remembered job is still running', async () => {
	const { apiClient } = slowBackend();
	const first = renderEditor(apiClient);
	await fireEvent.click(await first.findByRole('button', { name: /Regen/ }));
	await vi.waitFor(() => expect(first.getByRole('status')).toBeTruthy());
	first.unmount();

	const reloaded = renderEditor(apiClient);
	await vi.waitFor(() => expect(reloaded.getByRole('status').textContent).toContain('Generating'));
	expect(reloaded.getByRole('button', { name: /Regen/ }).disabled).toBe(true);
	expect(apiClient.generateCharacterReference).toHaveBeenCalledTimes(1);
});
