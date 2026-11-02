import test from 'node:test';
import assert from 'node:assert/strict';
import { MockApiClient } from '../src/lib/api/mockClient.js';
import { JOB_STATUS } from '../src/lib/api/types.js';

test('MockApiClient initializes with initial seed data', async () => {
	const client = new MockApiClient();
	const health = await client.checkHealth();
	assert.equal(health.status, 'ok');

	const projects = await client.listProjects();
	assert.equal(projects.length, 1);
	assert.equal(projects[0].title, 'The Quantum Detective');

	const credit = await client.getCreditUsage();
	assert.equal(credit.currency, 'USD');
	assert.ok(credit.remaining_credits > 0);
});

test('MockApiClient handles project CRUD operations', async () => {
	const client = new MockApiClient();

	const created = await client.createProject({
		title: 'Cyberpunk Chronicles',
		art_style: 'cyberpunk-noir',
		chapter_count: 2
	});
	assert.ok(created.id);
	assert.equal(created.title, 'Cyberpunk Chronicles');

	const fetched = await client.getProject(created.id);
	assert.equal(fetched.title, 'Cyberpunk Chronicles');

	const updated = await client.updateProject(created.id, { title: 'Updated Title' });
	assert.equal(updated.title, 'Updated Title');

	await client.deleteProject(created.id);
	await assert.rejects(async () => client.getProject(created.id), /not found/);
});

test('MockApiClient manages characters and approval cycle', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();

	const characters = await client.getCharacters(project.id);
	assert.ok(characters.length >= 1);
	const char = characters[0];
	assert.equal(char.name, 'Jack Vance');

	// Generating a reference image stales approval
	const { job, character } = await client.generateCharacterReference(char.id);
	assert.ok(job.id);
	assert.equal(character.approved_at, null);

	// User approves character
	const approved = await client.approveCharacter(char.id);
	assert.ok(approved.approved_at);
});

test('MockApiClient manages chapters, panels, and pages', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();

	const chapters = await client.getChapters(project.id);
	assert.ok(chapters.length >= 1);
	const chap = chapters[0];

	const panels = await client.getPanels(chap.id);
	assert.ok(panels.length >= 1);

	const pages = await client.getPages(chap.id);
	assert.ok(pages.length >= 1);
	const page = pages[0];

	// Draft cycle
	const draftRes = await client.generateDraft(page.id);
	assert.ok(draftRes.job.id);
	const approvedDraft = await client.approveDraft(page.id);
	assert.ok(approvedDraft.draft_approved_at);

	// Line art cycle
	const lineArtRes = await client.generateLineArt(page.id);
	assert.ok(lineArtRes.job.id);
	const approvedLineArt = await client.approveLineArt(page.id);
	assert.ok(approvedLineArt.line_art_approved_at);

	// Colour cycle
	const { palette } = await client.suggestColourPalette(page.id);
	assert.ok(palette.length > 0);
	const approvedColour = await client.approveColour(page.id);
	assert.ok(approvedColour.colour_approved_at);
});

test('MockApiClient calculates cost estimates based on stage and count', async () => {
	const client = new MockApiClient();
	const estimate = await client.estimateCost('proj-1', 'draft_sketch', 5);

	assert.equal(estimate.stage, 'draft_sketch');
	assert.equal(estimate.image_count, 5);
	assert.equal(estimate.estimated_cost_usd, 0.05); // 5 * $0.01
});

test('MockApiClient handles export workflow', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();

	const { job, export: exp } = await client.exportProject(project.id, {
		format: 'pdf',
		page_size: 'a4'
	});
	assert.ok(job.id);
	assert.ok(exp.id);
	assert.equal(exp.format, 'pdf');

	const fetched = await client.getExport(exp.id);
	assert.equal(fetched.status, 'ready');

	const downloadUrl = client.getExportDownloadUrl(exp.id);
	assert.ok(downloadUrl.includes(exp.id));
});
