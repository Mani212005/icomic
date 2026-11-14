import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { STAGES } from '../../src/lib/api/types.js';

const PROJECT = { id: 'project-1', title: 'Tide', art_style: 'cyberpunk-noir', original_script: 'A hero finds a bird.', chapter_count: 2 };
const CHAPTER = { id: 'chap-1', chapter_number: 1, title: 'The bird', summary: 'A bird', status: 'pending', updated_at: '2026-11-02T10:00:00Z' };
const PANEL = { id: 'panel-1', chapter_id: 'chap-1', panel_number: 1, scene_description: 'A bird', dialogue: 'Look!', narration: '', character_actions: '', camera_angle: '', background: '', image_prompt: '' };
const HERO = { id: 'char-1', name: 'Hero', description: 'Keeper', approved_at: null, updated_at: '2026-11-02T10:00:00Z', visual_attributes: { appearance: 'tall', clothing: 'coat' } };

beforeEach(() => {
	localStorage.clear();
	vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
});
afterEach(() => {
	cleanup();
	vi.useRealTimers();
});

function backend(overrides = {}) {
	return {
		getCharacters: async () => [HERO],
		getChapters: async () => [CHAPTER],
		getPanels: async () => [PANEL],
		getPages: async () => [],
		getColourPalette: async () => ({ palette: {} }),
		request: async () => ({}),
		getCreditUsage: async () => ({ remaining_credits: 5 }),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		updateProject: vi.fn(async () => PROJECT),
		updateCharacter: vi.fn(async () => ({})),
		updateChapter: vi.fn(async () => ({})),
		updatePanel: vi.fn(async () => ({})),
		...overrides
	};
}

const open = (apiClient, activeStage) => render(ComicEditor, { project: { ...PROJECT }, apiClient, activeStage });
const pause = (ms = 1000) => vi.advanceTimersByTimeAsync(ms);
const status = (view) => view.container.querySelector('.save-status');

it('saves the Script stage after a pause in typing, once, and says so', async () => {
	const apiClient = backend();
	const view = open(apiClient, STAGES.SCRIPT);
	const script = await view.findByLabelText('Script');
	await fireEvent.input(script, { target: { value: 'A hero finds a bird and a key.' } });
	await fireEvent.input(script, { target: { value: 'A hero finds a bird and a key. The door opens.' } });
	expect(status(view).textContent.trim()).toBe('Unsaved changes');
	expect(apiClient.updateProject).not.toHaveBeenCalled();

	await pause();
	expect(apiClient.updateProject).toHaveBeenCalledTimes(1);
	expect(apiClient.updateProject).toHaveBeenCalledWith('project-1', {
		title: 'Tide', art_style: 'cyberpunk-noir', chapter_count: 2, original_script: 'A hero finds a bird and a key. The door opens.'
	});
	await vi.waitFor(() => expect(status(view).textContent.trim()).toBe('Saved'));
});

it('keeps the last valid chapter count while the field is half-typed', async () => {
	const apiClient = backend();
	const view = open(apiClient, STAGES.SCRIPT);
	await fireEvent.input(await view.findByLabelText('Title'), { target: { value: 'Tides' } });
	await fireEvent.input(view.getByLabelText('Chapters'), { target: { value: '' } });
	await pause();
	expect(apiClient.updateProject.mock.calls[0][1]).toMatchObject({ title: 'Tides', chapter_count: 2 });
});

it('never lets a slow earlier save overwrite a newer edit', async () => {
	const pending = [];
	const apiClient = backend({ updateProject: vi.fn((id, draft) => new Promise((resolve) => pending.push({ draft, resolve }))) });
	const view = open(apiClient, STAGES.SCRIPT);
	const script = await view.findByLabelText('Script');
	await fireEvent.input(script, { target: { value: 'first' } });
	await pause();
	expect(pending.length).toBe(1);
	expect(status(view).textContent.trim()).toBe('Saving…');

	await fireEvent.input(script, { target: { value: 'second' } });
	await pause();
	expect(pending.length, 'the newer edit waits for the slow request').toBe(1);

	pending[0].resolve();
	await vi.waitFor(() => expect(pending.length).toBe(2));
	expect(pending[1].draft.original_script).toBe('second');
	expect(status(view).textContent.trim(), 'the first answer does not mean the second edit is saved').toBe('Saving…');
	pending[1].resolve();
	await vi.waitFor(() => expect(status(view).textContent.trim()).toBe('Saved'));
	expect(script.value).toBe('second');
});

it('shows a failed save with the reason and a Retry that saves again', async () => {
	let fail = true;
	const apiClient = backend({
		updateProject: vi.fn(async () => {
			if (fail) throw Object.assign(new Error('Script is empty'), { status: 400 });
			return PROJECT;
		})
	});
	const view = open(apiClient, STAGES.SCRIPT);
	await fireEvent.input(await view.findByLabelText('Script'), { target: { value: '' } });
	await pause();
	await vi.waitFor(() => expect(status(view).textContent).toContain('Not saved: Script is empty'));

	fail = false;
	await fireEvent.click(view.getByRole('button', { name: 'Retry' }));
	await vi.waitFor(() => expect(status(view).textContent.trim()).toBe('Saved'));
	expect(apiClient.updateProject).toHaveBeenCalledTimes(2);
});

it('autosaves a panel edit with only the changed fields, and Save closes without sending it twice', async () => {
	const apiClient = backend();
	const view = open(apiClient, STAGES.PANELS);
	await fireEvent.click(await view.findByRole('button', { name: /Edit/ }));
	await fireEvent.input(view.getByLabelText(/^Narration:/), { target: { value: 'Dusk' } });
	await pause();
	expect(apiClient.updatePanel).toHaveBeenCalledTimes(1);
	expect(apiClient.updatePanel).toHaveBeenCalledWith('panel-1', { narration: 'Dusk' });
	await vi.waitFor(() => expect(status(view).textContent.trim()).toBe('Saved'));

	await fireEvent.input(view.getByLabelText(/^Camera:/), { target: { value: 'High angle' } });
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(view.queryByRole('button', { name: 'Save' })).toBeNull());
	expect(apiClient.updatePanel).toHaveBeenCalledTimes(2);
	expect(apiClient.updatePanel).toHaveBeenLastCalledWith('panel-1', { camera_angle: 'High angle' });
});

it('keeps the panel form open when its save fails, and closes it after a retry succeeds', async () => {
	let fail = true;
	const apiClient = backend({
		updatePanel: vi.fn(async () => {
			if (fail) throw new Error('Network down');
		})
	});
	const view = open(apiClient, STAGES.PANELS);
	await fireEvent.click(await view.findByRole('button', { name: /Edit/ }));
	await fireEvent.input(view.getByLabelText(/^Narration:/), { target: { value: 'Dusk' } });
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(status(view).textContent).toContain('Not saved: Network down'));
	expect(view.getByLabelText(/^Narration:/).value).toBe('Dusk');

	fail = false;
	await fireEvent.click(view.getByRole('button', { name: 'Retry' }));
	await vi.waitFor(() => expect(status(view).textContent.trim()).toBe('Saved'));
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(view.queryByRole('button', { name: 'Save' })).toBeNull());
});

it('autosaves character and outline edits the way Save does', async () => {
	const apiClient = backend();
	let view = open(apiClient, STAGES.CHARACTERS);
	await fireEvent.click(await view.findByRole('button', { name: /Edit/ }));
	await fireEvent.input(view.getByLabelText(/Look/), { target: { value: 'broad shoulders' } });
	await pause();
	expect(apiClient.updateCharacter).toHaveBeenCalledWith('char-1', { name: 'Hero', description: 'Keeper', visual_attributes: { appearance: 'broad shoulders', clothing: 'coat' } });
	cleanup();

	view = open(apiClient, STAGES.CHAPTERS);
	await fireEvent.click(await view.findByRole('button', { name: /Edit/ }));
	await fireEvent.input(view.getByLabelText(/^Ending/), { target: { value: 'The door is locked' } });
	await pause();
	expect(apiClient.updateChapter).toHaveBeenCalledWith('chap-1', { title: 'The bird', summary: 'A bird', important_events: [], character_names: [], ending: 'The door is locked' });
});
