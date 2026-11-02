import test from 'node:test';
import assert from 'node:assert/strict';
import { MockApiClient } from '../src/lib/api/mockClient.js';
import { isApprovalStale, hasValidApproval } from '../src/lib/api/types.js';

test('isApprovalStale and hasValidApproval handle edge cases correctly', () => {
	// 1. Unapproved entity (approved_at is null or undefined)
	assert.equal(isApprovalStale(null, '2026-11-02T10:00:00Z'), false, 'null approval is unapproved, not stale');
	assert.equal(hasValidApproval(null, '2026-11-02T10:00:00Z'), false, 'null approval is not valid');

	// 2. Valid approval (approved_at is equal to or newer than updated_at)
	const t1 = '2026-11-02T10:00:00Z';
	const t2 = '2026-11-02T10:05:00Z';
	assert.equal(isApprovalStale(t2, t1), false, 'newer approval is valid and not stale');
	assert.equal(hasValidApproval(t2, t1), true, 'newer approval is valid');

	assert.equal(isApprovalStale(t1, t1), false, 'simultaneous approval is valid and not stale');
	assert.equal(hasValidApproval(t1, t1), true, 'simultaneous approval is valid');

	// 3. Stale approval (approved_at is strictly older than updated_at)
	assert.equal(isApprovalStale(t1, t2), true, 'approval older than update is stale');
	assert.equal(hasValidApproval(t1, t2), false, 'stale approval is not valid');

	// 4. Invalid timestamp strings
	assert.equal(isApprovalStale('invalid-date', t1), false);
	assert.equal(hasValidApproval('invalid-date', t1), false);
});

test('Proves and fixes earlier desync: editing character after approval stales approval', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();

	const [char] = await client.getCharacters(project.id);
	assert.ok(char.id);

	// Step 1: User approves character at T1
	const approved = await client.approveCharacter(char.id);
	assert.ok(approved.approved_at);
	assert.equal(hasValidApproval(approved.approved_at, approved.updated_at), true);

	// Step 2: Later, user edits character at T2 (T2 > T1)
	const updated = await client.updateCharacter(char.id, {
		description: 'Updated cybernetic eye description and personality traits'
	});

	// The old mock client desync defect:
	// Previously, mock client wiped approved_at = null on edits, which masked that the Go backend
	// keeps approved_at intact and only updates updated_at.
	// Meanwhile, the old frontend checked:
	//   allCharactersApproved = characters.every((c) => !!c.approved_at)
	// If approved_at was preserved, the old frontend falsely claimed "All Characters Approved",
	// but the Go backend returned HTTP 400 ErrStaleApproval!
	assert.ok(updated.approved_at, 'approved_at timestamp remains present matching Go backend');
	assert.ok(
		new Date(updated.updated_at).getTime() > new Date(updated.approved_at).getTime(),
		'updated_at is strictly newer than approved_at'
	);

	// Old buggy frontend check would have passed (false positive):
	const oldFrontendCheck = Boolean(updated.approved_at);
	assert.equal(oldFrontendCheck, true, 'proves earlier desync: old logic falsely treated character as approved');

	// New backend-matching check correctly identifies staleness:
	assert.equal(isApprovalStale(updated.approved_at, updated.updated_at), true);
	assert.equal(hasValidApproval(updated.approved_at, updated.updated_at), false);

	// Step 3: Re-approving character clears stale state
	const reapproved = await client.approveCharacter(char.id);
	assert.equal(isApprovalStale(reapproved.approved_at, reapproved.updated_at), false);
	assert.equal(hasValidApproval(reapproved.approved_at, reapproved.updated_at), true);
});

test('Editing a panel cascades to invalidate parent page draft approval', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();
	const [chap] = await client.getChapters(project.id);
	const [page] = await client.getPages(chap.id);
	const [panel] = await client.getPanels(chap.id);

	// Initially page draft is approved
	assert.ok(page.draft_approved_at);

	// Editing a panel invalidates draft approval on parent page
	await client.updatePanel(panel.id, {
		dialogue: 'Look out, the sentries are alerted!'
	});

	const [reloadedPage] = await client.getPages(chap.id);
	assert.equal(reloadedPage.draft_approved_at, null, 'draft approval was invalidated by panel edit');
	assert.equal(hasValidApproval(reloadedPage.draft_approved_at, reloadedPage.updated_at), false);
});

test('Regenerating draft sketch cascades to invalidate downstream line art approval', async () => {
	const client = new MockApiClient();
	const [project] = await client.listProjects();
	const [chap] = await client.getChapters(project.id);
	const [page] = await client.getPages(chap.id);

	// Initially page line art is approved
	assert.ok(page.line_art_approved_at);

	// Regenerating draft invalidates line art approval
	const { page: draftPage } = await client.generateDraft(page.id);
	assert.equal(draftPage.line_art_approved_at, null, 'line art approval invalidated by draft regeneration');
	assert.equal(hasValidApproval(draftPage.line_art_approved_at, draftPage.updated_at), false);
});
