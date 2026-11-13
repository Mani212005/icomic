import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { STAGES } from '../../src/lib/api/types.js';

const PROJECT = { id: 'project-1', title: 'Tide', original_script: 'A hero.', chapter_count: 1 };

beforeEach(() => localStorage.clear());
afterEach(cleanup);

// A backend whose job stays unfinished until the test moves it, and whose cancel answers the way
// the server does: `cancelled` for a job that has not reached the model, `settling` for one that has.
function backend(initialStatus, cancelOutcome) {
	const state = { image: '', status: initialStatus, cancels: [] };
	const apiClient = {
		async getCharacters() {
			return [{ id: 'char-1', name: 'Hero', description: 'Keeper', reference_image_url: state.image }];
		},
		getChapters: async () => [],
		getCreditUsage: async () => ({ remaining_credits: 5 }),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		generateCharacterReference: vi.fn(async (id) => ({ job: { id: 'job-1', status: 'pending' }, job_id: 'job-1', entity_type: 'character', entity_id: id })),
		async getJob(jobId) {
			return { id: jobId, status: state.status };
		},
		cancelJob: vi.fn(async (jobId) => {
			state.cancels.push(jobId);
			if (cancelOutcome === 'cancelled') state.status = 'cancelled';
			return { outcome: cancelOutcome, job: { id: jobId, status: state.status }, refunded: cancelOutcome === 'cancelled' ? 0.01 : 0 };
		})
	};
	return { state, apiClient };
}

function renderEditor(apiClient) {
	return render(ComicEditor, { project: PROJECT, apiClient, activeStage: STAGES.CHARACTERS });
}

async function startGeneration(view) {
	await fireEvent.click(await view.findByRole('button', { name: /Regen/ }));
	return view.findByRole('button', { name: 'Cancel generation' });
}

it('cancelling a job that has not reached the model says nothing was charged and unlocks the controls', async () => {
	const { state, apiClient } = backend('pending', 'cancelled');
	const view = renderEditor(apiClient);
	const cancel = await startGeneration(view);
	expect(view.getByRole('button', { name: /Regen/ }).disabled).toBe(true);

	await fireEvent.click(cancel);

	await vi.waitFor(() => expect(view.getByRole('status').textContent).toContain('Cancelled, nothing was charged.'));
	expect(state.cancels).toEqual(['job-1']);
	expect(view.queryByRole('button', { name: 'Cancel generation' })).toBeNull();
	expect(view.getByRole('button', { name: /Regen/ }).disabled).toBe(false);
	expect(view.queryByText(/Generating/)).toBeNull();
	expect(localStorage.getItem('icomic.activeJob.project-1')).toBeNull();
	await fireEvent.click(view.getByRole('button', { name: 'Dismiss' }));
	expect(view.queryByRole('status')).toBeNull();
});

it('cancelling a job already with the model shows settling, keeps the controls locked, and never says it was stopped', async () => {
	const { state, apiClient } = backend('running', 'settling');
	const view = renderEditor(apiClient);
	const cancel = await startGeneration(view);

	await fireEvent.click(cancel);

	await vi.waitFor(() => expect(view.getByRole('status').textContent).toContain('Already with the model, finishing. You will be charged.'));
	const banner = view.getByRole('status');
	expect(banner.getAttribute('data-state')).toBe('settling');
	expect(banner.textContent).not.toMatch(/cancelled|stopped/i);
	expect(view.queryByRole('button', { name: 'Cancel generation' })).toBeNull();
	expect(view.getByRole('button', { name: /Regen/ }).disabled).toBe(true);
	expect(apiClient.generateCharacterReference).toHaveBeenCalledTimes(1);

	// The job reaches its own terminal state with its result; only then do the controls unlock.
	state.image = 'https://images.example/hero.png';
	state.status = 'done';
	const image = await view.findByRole('img', { name: 'Hero' }, { timeout: 3000 });
	expect(image.getAttribute('src')).toBe('https://images.example/hero.png');
	await vi.waitFor(() => expect(view.getByRole('status').textContent).toContain('you were charged'));
	expect(view.getByRole('button', { name: /Regen/ }).disabled).toBe(false);
	expect(state.cancels).toEqual(['job-1']);
});
