import test from 'node:test';
import assert from 'node:assert/strict';
import {
	calculateAge,
	DefaultAgeGatePolicy,
	DefaultLocationPolicy,
	DefaultImageHostingPolicy
} from '../src/lib/config/policies.js';

test('calculateAge calculates correct age across regular years', () => {
	const dob = '2000-05-15';
	assert.equal(calculateAge(dob, new Date('2018-05-14T00:00:00Z')), 17);
	assert.equal(calculateAge(dob, new Date('2018-05-15T00:00:00Z')), 18);
	assert.equal(calculateAge(dob, new Date('2026-01-01T00:00:00Z')), 25);
});

test('calculateAge handles leap year birthdates accurately without drift', () => {
	// Born on leap day: 2008-02-29
	const leapDob = '2008-02-29';

	// On 2026-02-28, they have not yet reached March 1 (17 full years)
	assert.equal(calculateAge(leapDob, new Date('2026-02-28T00:00:00Z')), 17);

	// On 2026-03-01, they have reached age 18
	assert.equal(calculateAge(leapDob, new Date('2026-03-01T00:00:00Z')), 18);

	// On 2012-02-28 (a leap year), they are 3 years old
	assert.equal(calculateAge(leapDob, new Date('2012-02-28T00:00:00Z')), 3);
	// On 2012-02-29 (4th birthday), they are 4 years old
	assert.equal(calculateAge(leapDob, new Date('2012-02-29T00:00:00Z')), 4);
});

test('DefaultAgeGatePolicy enforces 18+ requirement with clear diagnostics', () => {
	const policy = new DefaultAgeGatePolicy(18);

	const underageResult = policy.verify('2010-06-01', new Date('2026-01-01T00:00:00Z'));
	assert.equal(underageResult.allowed, false);
	assert.equal(underageResult.age, 15);
	assert.match(underageResult.reason, /under the minimum age requirement/);

	const adultResult = policy.verify('2000-01-01', new Date('2026-01-01T00:00:00Z'));
	assert.equal(adultResult.allowed, true);
	assert.equal(adultResult.age, 26);
	assert.equal(adultResult.reason, undefined);

	const missingResult = policy.verify(null);
	assert.equal(missingResult.allowed, false);
	assert.match(missingResult.reason, /Date of birth is required/);
});

test('DefaultLocationPolicy allows safe default and enforces pluggable restrictions', () => {
	const policy = new DefaultLocationPolicy({
		blockedRegions: ['KP'],
		restrictedImageRegions: ['IN']
	});

	assert.deepEqual(policy.canAccessService('US'), { allowed: true });
	assert.deepEqual(policy.canAccessService('KP'), {
		allowed: false,
		reason: 'Service is not available in country code KP.'
	});

	assert.deepEqual(policy.canGenerateImages('US'), { allowed: true });
	assert.deepEqual(policy.canGenerateImages('IN'), {
		allowed: false,
		reason: 'Image generation is currently restricted for region IN pending captain determination.'
	});
});

test('DefaultImageHostingPolicy resolves relative and absolute asset paths', () => {
	const policy = new DefaultImageHostingPolicy('https://cdn.icomic.local/assets');

	assert.equal(policy.resolveImageUrl('https://external.local/art.png'), 'https://external.local/art.png');
	assert.equal(policy.resolveImageUrl('data:image/png;base64,123'), 'data:image/png;base64,123');
	assert.equal(policy.resolveImageUrl('drafts/page1.png'), 'https://cdn.icomic.local/assets/drafts/page1.png');
	assert.equal(policy.resolveImageUrl('/drafts/page1.png'), 'https://cdn.icomic.local/assets/drafts/page1.png');
	assert.equal(policy.resolveImageUrl(''), '');
});
