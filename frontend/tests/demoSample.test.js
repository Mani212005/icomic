import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { MockApiClient } from '../src/lib/api/mockClient.js';

const root = fileURLToPath(new URL('../', import.meta.url));

test('MockApiClient seeds an approved real sample instead of crude mock art', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();
	assert.ok(project, 'Seeded project exists');

	const pages = await client.getPages('chap-1');
	assert.ok(pages.length > 0, 'Seeded page exists');
	const page = pages[0];

	// Proves defect: crude mock SVG /mock/line-art.svg must not be served as the demo line art
	assert.notEqual(
		page.line_art_url,
		'/mock/line-art.svg',
		'Demo must not serve crude mock SVG line-art.svg'
	);
	assert.equal(
		page.line_art_url,
		'/mock/sample-line-art.webp',
		'Demo page must point to approved real sample line art'
	);
	assert.equal(page.is_sample, true, 'Page must be identified with is_sample metadata');
	assert.ok(page.sample_label, 'Page must include descriptive sample_label');

	const characters = await client.getCharacters(project.id);
	assert.ok(characters.length > 0, 'Seeded character exists');
	const character = characters[0];

	assert.notEqual(
		character.reference_image_url,
		'/mock/character.svg',
		'Demo character must not serve crude mock SVG character.svg'
	);
	assert.equal(
		character.reference_image_url,
		'/mock/sample-character.webp',
		'Demo character must point to approved real sample character'
	);
	assert.equal(character.is_sample, true, 'Character must be identified with is_sample metadata');

	assert.notEqual(
		page.draft_image_url,
		'/mock/draft.svg',
		'Demo draft must not serve crude mock SVG draft.svg'
	);
	assert.equal(
		page.draft_image_url,
		'/mock/sample-draft.webp',
		'Demo draft must point to approved real sample draft'
	);
});

test('Sample image assets exist on disk and meet bundled size budget (< 100 KB)', () => {
	const sampleFiles = [
		'sample-line-art.webp',
		'sample-character.webp',
		'sample-draft.webp'
	];

	for (const filename of sampleFiles) {
		const filePath = join(root, 'static/mock', filename);
		assert.ok(existsSync(filePath), `Sample asset static/mock/${filename} must exist`);
		const size = statSync(filePath).size;
		assert.ok(size > 1000, `Sample asset static/mock/${filename} must not be empty`);
		assert.ok(size < 100_000, `Sample asset static/mock/${filename} (${size} B) must be < 100 KB`);
	}
});

test('MockApiClient regeneration preserves representative sample outputs', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();
	const [page] = await client.getPages('chap-1');
	const [char] = await client.getCharacters(project.id);

	const { page: regDraft } = await client.generateDraft(page.id);
	assert.equal(regDraft.draft_image_url, '/mock/sample-draft.webp');

	const { page: regLine } = await client.generateLineArt(page.id);
	assert.equal(regLine.line_art_url, '/mock/sample-line-art.webp');

	const { character: regChar } = await client.generateCharacterReference(char.id);
	assert.equal(regChar.reference_image_url, '/mock/sample-character.webp');
});

test('Frontend UI templates render prominent sample banner and sample tag in mock mode', () => {
	const pageTemplate = readFileSync(join(root, 'src/routes/+page.svelte'), 'utf8');
	assert.ok(
		pageTemplate.includes('demo-sample-banner'),
		'+page.svelte must include prominent demo-sample-banner in mock mode'
	);

	const editorTemplate = readFileSync(join(root, 'src/lib/editor/ComicEditor.svelte'), 'utf8');
	assert.ok(
		editorTemplate.includes('sample-tag') || editorTemplate.includes('sample-badge'),
		'ComicEditor.svelte must render sample indicator on sample page previews'
	);
});
