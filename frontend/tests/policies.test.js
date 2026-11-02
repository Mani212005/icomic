import test from 'node:test';
import assert from 'node:assert/strict';
import {
	AGE_TERMS_ADVISORY,
	DefaultImageHostingPolicy
} from '../src/lib/config/policies.js';

test('AGE_TERMS_ADVISORY provides advisory notice without enforced blocking', () => {
	assert.ok(AGE_TERMS_ADVISORY.includes('18 years of age or older'));
});

test('DefaultImageHostingPolicy resolves relative and absolute asset paths', () => {
	const policy = new DefaultImageHostingPolicy('https://cdn.icomic.local/assets');

	assert.equal(policy.resolveImageUrl('https://external.local/art.png'), 'https://external.local/art.png');
	assert.equal(policy.resolveImageUrl('data:image/png;base64,123'), 'data:image/png;base64,123');
	assert.equal(policy.resolveImageUrl('drafts/page1.png'), 'https://cdn.icomic.local/assets/drafts/page1.png');
	assert.equal(policy.resolveImageUrl('/drafts/page1.png'), 'https://cdn.icomic.local/assets/drafts/page1.png');
	assert.equal(policy.resolveImageUrl(''), '');
});
