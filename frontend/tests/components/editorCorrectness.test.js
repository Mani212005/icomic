import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { STAGES } from '../../src/lib/api/types.js';

const PROJECT = { id: 'project-1', title: 'Tide', original_script: 'A hero finds a bird.', chapter_count: 1 };
const APPROVED = '2026-11-02T10:05:00Z';
const EARLIER = '2026-11-02T10:00:00Z';
const LATER = '2026-11-02T10:10:00Z';

beforeEach(() => localStorage.clear());
afterEach(cleanup);

const HERO = { id: 'char-1', name: 'Hero', description: 'Keeper', reference_image_url: '/hero.png', approved_at: null, updated_at: EARLIER, visual_attributes: { age_range: '20-25', hair_style: 'teal', appearance: 'tall', clothing: 'coat', reference_role: 'main' } };
// The shape the Go API returns: a chapter has a status and no approved_at.
const CHAPTER = { id: 'chap-1', chapter_number: 1, title: 'The bird', summary: 'A bird leads the way', important_events: ['Hero finds the bird', 'The storm arrives'], character_names: ['Hero', 'Bird'], ending: 'The door opens', status: 'pending', updated_at: EARLIER };
const PANEL = { id: 'panel-1', chapter_id: 'chap-1', panel_number: 1, scene_description: 'A bird', character_actions: 'Hero kneels', camera_angle: 'Low angle', background: 'Dunes', dialogue: 'Hero: Look!', narration: 'Dawn', image_prompt: 'A bird on a dune' };

function backend({ characters = [HERO], chapters = [], panels = [], pages = [] } = {}) {
	const calls = { updateCharacter: [], updateChapter: [], updatePanel: [] };
	const apiClient = {
		getCharacters: async () => characters,
		getChapters: async () => chapters,
		getPanels: async () => panels,
		getPages: async () => pages,
		getColourPalette: async () => ({ palette: {} }),
		request: async () => ({}),
		getCreditUsage: async () => ({ remaining_credits: 5 }),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true }),
		updateProject: vi.fn(async () => PROJECT),
		analyzeScript: vi.fn(async () => ({ job_id: 'job-1', character_profiles: [], warnings: [] })),
		getJob: async (jobId) => ({ id: jobId, status: 'done' }),
		updateCharacter: vi.fn(async (id, data) => (calls.updateCharacter.push(data), {})),
		updateChapter: vi.fn(async (id, data) => (calls.updateChapter.push(data), {})),
		updatePanel: vi.fn(async (id, data) => (calls.updatePanel.push(data), {}))
	};
	return { apiClient, calls };
}

const open = (apiClient, activeStage) => render(ComicEditor, { project: { ...PROJECT }, apiClient, activeStage });

// -- never approved is pending, not stale ---------------------------------------------------

it('shows freshly analysed, never-approved characters as pending: no Stale tag, banner or Edited label', async () => {
	const { apiClient } = backend();
	const view = open(apiClient, STAGES.CHARACTERS);
	await view.findByText('Hero');
	expect(view.getByText('Pending')).toBeTruthy();
	expect(view.queryByText('Stale')).toBeNull();
	expect(view.queryByText('Edited')).toBeNull();
	expect(document.querySelector('.stale-stage-banner')).toBeNull();
	expect(view.getByRole('button', { name: 'Approve' })).toBeTruthy();
});

it('shows a character edited after its approval as stale, with a banner that says what to do', async () => {
	const { apiClient } = backend({ characters: [{ ...HERO, approved_at: APPROVED, updated_at: LATER }] });
	const view = open(apiClient, STAGES.CHARACTERS);
	await view.findByText('Hero');
	expect(view.getByText('Stale')).toBeTruthy();
	expect(view.getByText('Edited')).toBeTruthy();
	const banner = document.querySelector('.stale-stage-banner');
	expect(banner.textContent).toContain('Changed since approval');
	expect(banner.textContent).toContain('Approve again');
});

it('shows a current approval as approved and unlocks Continue', async () => {
	const { apiClient } = backend({ characters: [{ ...HERO, approved_at: APPROVED }] });
	const view = open(apiClient, STAGES.CHARACTERS);
	await view.findByText('Hero');
	expect(view.queryByText('Stale')).toBeNull();
	expect(view.getAllByText('Approved').length).toBeGreaterThan(0);
	expect(view.getByRole('button', { name: /Continue/ }).disabled).toBe(false);
});

it('treats a pending outline as pending and an approved one (status, as the server sends it) as approved', async () => {
	const pending = backend({ chapters: [CHAPTER] });
	let view = open(pending.apiClient, STAGES.CHAPTERS);
	await view.findByText(/Chapter 1: The bird/);
	expect(view.getByText('Pending')).toBeTruthy();
	expect(view.queryByText('Stale')).toBeNull();
	expect(view.queryByText('Edited')).toBeNull();
	expect(document.querySelector('.stale-stage-banner')).toBeNull();
	expect(view.getByRole('button', { name: /Continue/ }).disabled).toBe(true);
	cleanup();

	const approved = backend({ chapters: [{ ...CHAPTER, status: 'approved' }] });
	view = open(approved.apiClient, STAGES.CHAPTERS);
	await view.findByText(/Chapter 1: The bird/);
	expect(view.queryByText('Stale')).toBeNull();
	expect(view.getAllByText('Approved').length).toBeGreaterThan(0);
	expect(view.getByRole('button', { name: /Continue/ }).disabled).toBe(false);
});

const PAGE = { id: 'page-1', chapter_id: 'chap-1', page_number: 1, panel_ids: ['panel-1'], draft_image_url: '/draft.png', draft_approved_at: null, updated_at: EARLIER, line_art_image_url: '/line.png', line_art_approved_at: null };

it('draft and finish stages show never-approved pages as pending, and an edited approved page as stale with text', async () => {
	let view = open(backend({ chapters: [CHAPTER], panels: [PANEL], pages: [PAGE] }).apiClient, STAGES.DRAFT);
	await view.findByAltText('Draft 1');
	expect(view.getByText('Pending')).toBeTruthy();
	expect(view.queryByText('Stale')).toBeNull();
	expect(view.queryByText('Edited')).toBeNull();
	expect(document.querySelector('.stale-stage-banner')).toBeNull();
	cleanup();

	const edited = { ...PAGE, draft_approved_at: APPROVED, updated_at: LATER };
	view = open(backend({ chapters: [CHAPTER], panels: [PANEL], pages: [edited] }).apiClient, STAGES.DRAFT);
	await view.findByAltText('Draft 1');
	expect(view.getByText('Stale')).toBeTruthy();
	expect(document.querySelector('.stale-stage-banner').textContent).toContain('Changed since approval');
	cleanup();

	view = open(backend({ chapters: [CHAPTER], panels: [PANEL], pages: [PAGE] }).apiClient, STAGES.LINE_ART);
	await view.findByText('#1');
	expect(view.getByText('Pending')).toBeTruthy();
	expect(view.queryByText('Stale')).toBeNull();
	expect(document.querySelector('.stale-stage-banner')).toBeNull();
	cleanup();

	const finishEdited = { ...PAGE, line_art_approved_at: APPROVED, updated_at: LATER };
	view = open(backend({ chapters: [CHAPTER], panels: [PANEL], pages: [finishEdited] }).apiClient, STAGES.LINE_ART);
	await view.findByText('#1');
	expect(view.getByText('Stale')).toBeTruthy();
	expect(document.querySelector('.stale-stage-banner').textContent).toContain('Approve again');
});

// -- character edits send only the keys the form edits ----------------------------------------

it('saves a character with name, description and only the appearance and clothing attributes', async () => {
	const { apiClient, calls } = backend();
	const view = open(apiClient, STAGES.CHARACTERS);
	await fireEvent.click(await view.findByRole('button', { name: /Edit/ }));
	await fireEvent.input(view.getByLabelText(/Look/), { target: { value: 'broad shoulders' } });
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(calls.updateCharacter.length).toBe(1));
	expect(calls.updateCharacter[0]).toEqual({
		name: 'Hero',
		description: 'Keeper',
		visual_attributes: { appearance: 'broad shoulders', clothing: 'coat' }
	});
});

// -- every panel field is editable -------------------------------------------------------------

it('edits narration, actions, camera, background and prompt as well as scene and dialogue, sending only what changed', async () => {
	const { apiClient, calls } = backend({ chapters: [{ ...CHAPTER, status: 'approved' }], panels: [PANEL] });
	const view = open(apiClient, STAGES.PANELS);
	await view.findByText(/Panel 1/);
	expect(view.getByText('Hero kneels')).toBeTruthy();
	expect(view.getByText('Low angle')).toBeTruthy();
	expect(view.getByText('Dunes')).toBeTruthy();

	await fireEvent.click(view.getByRole('button', { name: /Edit/ }));
	for (const label of ['Scene', 'Actions', 'Camera', 'Background', 'Dialogue', 'Narration', 'Prompt']) {
		expect(view.getByLabelText(new RegExp(`^${label}:`))).toBeTruthy();
	}
	await fireEvent.input(view.getByLabelText(/^Narration:/), { target: { value: 'Dusk' } });
	await fireEvent.input(view.getByLabelText(/^Camera:/), { target: { value: 'High angle' } });
	await fireEvent.input(view.getByLabelText(/^Prompt:/), { target: { value: 'A bird at dusk' } });
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(calls.updatePanel.length).toBe(1));
	expect(calls.updatePanel[0]).toEqual({ narration: 'Dusk', camera_angle: 'High angle', image_prompt: 'A bird at dusk' });
});

it('saving a panel without changing anything sends nothing, so page approvals are not undone', async () => {
	const { apiClient, calls } = backend({ chapters: [{ ...CHAPTER, status: 'approved' }], panels: [PANEL] });
	const view = open(apiClient, STAGES.PANELS);
	await view.findByText(/Panel 1/);
	await fireEvent.click(view.getByRole('button', { name: /Edit/ }));
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(view.queryByRole('button', { name: 'Save' })).toBeNull());
	expect(calls.updatePanel).toEqual([]);
});

// -- the outline's events, cast and ending are shown and editable ----------------------------

it('shows the outline events, characters and ending, and saves edits to them', async () => {
	const { apiClient, calls } = backend({ chapters: [CHAPTER] });
	const view = open(apiClient, STAGES.CHAPTERS);
	await view.findByText(/Chapter 1: The bird/);
	expect(view.getByText('Hero finds the bird')).toBeTruthy();
	expect(view.getByText('Hero, Bird')).toBeTruthy();
	expect(view.getByText('The door opens')).toBeTruthy();

	await fireEvent.click(view.getByRole('button', { name: /Edit/ }));
	await fireEvent.input(view.getByLabelText(/^Events/), { target: { value: 'Hero finds the bird\n\n  The storm arrives early  \n' } });
	await fireEvent.input(view.getByLabelText(/^Cast/), { target: { value: 'Hero' } });
	await fireEvent.input(view.getByLabelText(/^Ending/), { target: { value: 'The door is locked' } });
	await fireEvent.click(view.getByRole('button', { name: 'Save' }));
	await vi.waitFor(() => expect(calls.updateChapter.length).toBe(1));
	expect(calls.updateChapter[0]).toEqual({
		title: 'The bird',
		summary: 'A bird leads the way',
		important_events: ['Hero finds the bird', 'The storm arrives early'],
		character_names: ['Hero'],
		ending: 'The door is locked'
	});
});

// -- analyze warnings ------------------------------------------------------------------------

it('shows the warnings the analysis returned on the Characters stage it leads to, and clears them on the next run', async () => {
	const { apiClient } = backend();
	apiClient.analyzeScript
		.mockResolvedValueOnce({ job_id: 'job-1', warnings: ['40 chapters is a lot for a 900-word script: each chapter would get about 5 panels.'] })
		.mockResolvedValueOnce({ job_id: 'job-2', warnings: [] });
	const view = open(apiClient, STAGES.SCRIPT);
	await fireEvent.click(await view.findByRole('button', { name: /Analyze/ }));

	const notes = await view.findByRole('list', { name: 'Notes' }, { timeout: 4000 });
	expect(notes.textContent).toContain('40 chapters is a lot for a 900-word script');
	expect(view.getByText('Hero')).toBeTruthy();

	await view.rerender({ activeStage: STAGES.SCRIPT });
	await fireEvent.click(await view.findByRole('button', { name: /Analyze/ }));
	await vi.waitFor(() => expect(view.queryByRole('list', { name: 'Notes' })).toBeNull(), { timeout: 4000 });
});

// -- the script box shows, and analyzes, the stored script ----------------------------------

it('shows the stored script in the Script box and analyses what was edited there', async () => {
	const { apiClient } = backend();
	const view = open(apiClient, STAGES.SCRIPT);
	const box = await view.findByLabelText('Script');
	expect(box.value).toBe(PROJECT.original_script);

	await fireEvent.input(box, { target: { value: 'A hero finds a bird and a key.' } });
	await fireEvent.click(view.getByRole('button', { name: /Analyze/ }));
	await vi.waitFor(() => expect(apiClient.analyzeScript).toHaveBeenCalledTimes(1));
	expect(apiClient.analyzeScript.mock.calls[0][1]).toBe('A hero finds a bird and a key.');
	expect(apiClient.updateProject.mock.calls[0][1]).toMatchObject({ original_script: 'A hero finds a bird and a key.' });
});
