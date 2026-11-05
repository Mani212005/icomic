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
	assert.equal(page.panel_ids.length, 4, 'Sample page must show all four beats of the lighthouse story');
	assert.deepEqual(page.panel_ids, ['panel-1', 'panel-2', 'panel-3', 'panel-4']);

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

	const { page: regLine } = await client.generateLineArt(page.id, 'ink');
	assert.equal(regLine.line_art_url, '/mock/sample-line-art.webp');

	const { character: regChar } = await client.generateCharacterReference(char.id);
	assert.equal(regChar.reference_image_url, '/mock/sample-character.webp');
});

test('Mock character image and description reference the same coherent sample', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();
	assert.ok(project, 'Seeded project exists');
	assert.equal(project.title, 'The Lighthouse Bird', 'Project title must match the lighthouse bird sample');
	assert.equal(project.art_style, 'indie-graphic-novel', 'Art style must match the selected editor style');
	assert.equal(project.is_sample, true, 'Project must carry is_sample metadata');
	assert.ok(project.sample_label, 'Project must include sample_label');

	const characters = await client.getCharacters(project.id);
	assert.ok(characters.length > 0, 'Seeded character exists');
	const character = characters[0];

	assert.equal(character.name, 'Nia', 'Demo character must be the lighthouse keeper in the sample story');
	assert.match(character.description, /lighthouse keeper.*brass bird/i, 'Character description must describe Nia and the brass bird');
	assert.match(character.appearance, /dark curly hair.*freckles/i, 'Character appearance must match the reference image');
	assert.match(character.clothing, /navy.*coat.*cream sweater/i, 'Character clothing must match the reference image');
	assert.equal(
		character.reference_image_url,
		'/mock/sample-character.webp',
		'Character reference must point to approved sample portrait'
	);
	assert.equal(character.is_sample, true, 'Character must be marked as sample');
	assert.ok(character.sample_label, 'Character must retain sample label');
	assert.match(project.original_script, /^Nia, a young lighthouse keeper/);
	assert.match(project.original_script, /At sunrise, Nia sets it free/);
	const [chapter] = await client.getChapters(project.id);
	assert.match(chapter.summary, /Nia.*brass bird.*lost boat.*sunrise/i);
	const [page] = await client.getPages(chapter.id);
	const panels = await client.getPanels(chapter.id);
	assert.equal(panels.length, 4, 'Script beats must map to four sample panels');
	assert.match(panels[0].scene_description, /Nia.*brass bird.*lighthouse/i);
	assert.match(panels[1].dialogue, /^Nia:/, 'Model-baked speaker label stays in sample dialogue');
	assert.match(panels[2].dialogue, /^Sailor:/, 'Model-baked speaker label stays in sample dialogue');
	assert.match(panels[3].scene_description, /sunrise.*Nia.*bird/i);
	assert.deepEqual(page.panel_ids, panels.map((panel) => panel.id));

	// Verify sample portrait file on disk is under 100 KB budget and clean portrait
	const charAssetPath = join(root, 'static/mock/sample-character.webp');
	assert.ok(existsSync(charAssetPath), 'sample-character.webp must exist');
	const charAssetSize = statSync(charAssetPath).size;
	assert.ok(charAssetSize > 1000, 'sample-character.webp must not be empty');
	assert.ok(charAssetSize < 100_000, `sample-character.webp (${charAssetSize} B) must be < 100 KB`);

	// Verify the template styles character images with object-fit contain
	const themeCss = readFileSync(join(root, 'src/lib/design/theme.css'), 'utf8');
	assert.ok(
		themeCss.includes('.char-img') && themeCss.includes('object-fit: contain'),
		'theme.css must style .char-img with object-fit: contain'
	);
});

test('Frontend UI templates render sample indicator on sample assets in mock mode', () => {
	const pageTemplate = readFileSync(join(root, 'src/routes/+page.svelte'), 'utf8');
	assert.ok(
		pageTemplate.includes('mock-pill'),
		'+page.svelte must identify mock mode with status pill'
	);

	const editorTemplate = readFileSync(join(root, 'src/lib/editor/ComicEditor.svelte'), 'utf8');
	assert.ok(
		editorTemplate.includes('sample-tag') || editorTemplate.includes('sample-badge'),
		'ComicEditor.svelte must render sample indicator on sample page previews'
	);
});
