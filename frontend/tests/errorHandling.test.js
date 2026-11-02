import test from 'node:test';
import assert from 'node:assert/strict';
import { formatApiError } from '../src/lib/api/types.js';

test('formatApiError formats 400 stale approval errors with actionable guidance', () => {
	const err1 = new Error('approval is stale due to subsequent edit or regeneration');
	err1.status = 400;
	const formatted1 = formatApiError(err1);
	assert.equal(formatted1.type, 'stale_approval');
	assert.equal(formatted1.status, 400);
	assert.match(formatted1.title, /Approval Gate \/ Stale Approval/);
	assert.match(formatted1.action, /Re-approve/);

	const err2 = new Error('character Jack Vance was modified after approval');
	err2.status = 400;
	const formatted2 = formatApiError(err2);
	assert.equal(formatted2.type, 'stale_approval');

	const err3 = new Error('chapter outline must be approved before generating draft sketch');
	err3.status = 400;
	const formatted3 = formatApiError(err3);
	assert.equal(formatted3.type, 'stale_approval');
});

test('formatApiError formats 503 kill switch and service unavailable errors', () => {
	const err1 = new Error('global image generation kill switch is active');
	err1.status = 503;
	const formatted1 = formatApiError(err1);
	assert.equal(formatted1.type, 'kill_switch');
	assert.equal(formatted1.status, 503);
	assert.match(formatted1.title, /Global Kill Switch Active/);
	assert.match(formatted1.action, /kill switch/);

	const err2 = new Error('AI service not available');
	err2.status = 503;
	const formatted2 = formatApiError(err2);
	assert.equal(formatted2.type, 'kill_switch');
});

test('formatApiError formats 402 and 429 quota and credit limit errors', () => {
	const err1 = new Error('user image generation quota exceeded: daily image limit reached');
	err1.status = 429;
	const formatted1 = formatApiError(err1);
	assert.equal(formatted1.type, 'quota_exceeded');
	assert.equal(formatted1.status, 429);
	assert.match(formatted1.title, /Usage Quota \/ Balance Exceeded/);
	assert.match(formatted1.action, /quota has been reached/);

	const err2 = new Error('Debit failed: insufficient credits');
	err2.status = 402;
	const formatted2 = formatApiError(err2);
	assert.equal(formatted2.type, 'quota_exceeded');
	assert.equal(formatted2.status, 402);
});

test('formatApiError formats moderation rejections', () => {
	const err1 = new Error('Prompt moderation failed: banned keyword detected');
	err1.status = 400;
	const formatted1 = formatApiError(err1);
	assert.equal(formatted1.type, 'moderation_rejected');
	assert.equal(formatted1.status, 400);
	assert.match(formatted1.title, /Content Policy Rejection/);
	assert.match(formatted1.action, /safety moderation/);
});

test('formatApiError formats 401 unauthenticated errors', () => {
	const err1 = new Error('unauthorized: authentication required');
	err1.status = 401;
	const formatted1 = formatApiError(err1);
	assert.equal(formatted1.type, 'unauthorized');
	assert.equal(formatted1.status, 401);
	assert.match(formatted1.title, /Authentication Required/);
	assert.match(formatted1.action, /dev mode/);
});

test('formatApiError handles fallback and null values gracefully', () => {
	assert.equal(formatApiError(null), null);
	assert.equal(formatApiError(undefined), null);

	const generic = formatApiError(new Error('Network disconnected'));
	assert.equal(generic.type, 'generic');
	assert.equal(generic.message, 'Network disconnected');
});
