/**
 * Pluggable policy seams for open captain decisions in Icomic.
 *
 * NOTE: Open captain decisions (user location / India restriction for image generation,
 * image hosting provider, and the 18+ age gate verification mechanism) must be modeled
 * as pluggable policy seams with safe defaults and documented TODOs, never hard-coded.
 *
 * References:
 * - docs/SPEC.md: Section 1.3, Section 1.4, Section 11.2, Section 11.3
 * - docs/ARCHITECTURE.md: Section 9.6, Section 9.9, Section 9.10
 */

/**
 * 1. Age Gate Policy Seam
 *
 * TODO (Captain Decision Pending):
 * Determine the specific age verification mechanism for production:
 * - Self-declaration checkbox during onboarding / sign-up (minimum viable check)
 * - Government ID verification (e.g. Persona, Stripe Identity)
 * - Payment method age inference (credit card holder verification)
 *
 * Current Safe Default:
 * Deterministic calendar-based age calculation requiring age >= 18.
 * Correctly accounts for calendar boundaries and leap years without leap-year drift.
 */
export function calculateAge(dob, referenceDate = new Date()) {
	const birth = dob instanceof Date ? dob : new Date(dob);
	const now = referenceDate instanceof Date ? referenceDate : new Date(referenceDate);

	if (isNaN(birth.getTime()) || isNaN(now.getTime())) {
		throw new Error('Invalid date provided to calculateAge');
	}

	let age = now.getUTCFullYear() - birth.getUTCFullYear();
	const monthDiff = now.getUTCMonth() - birth.getUTCMonth();
	const dayDiff = now.getUTCDate() - birth.getUTCDate();

	if (monthDiff < 0 || (monthDiff === 0 && dayDiff < 0)) {
		age--;
	}
	return age;
}

export class DefaultAgeGatePolicy {
	constructor(minAge = 18) {
		this.minAge = minAge;
	}

	/**
	 * Verify if a user meets the age requirement.
	 * @param {string|Date} dateOfBirth
	 * @param {Date} [referenceDate]
	 * @returns {{ allowed: boolean, age: number, reason?: string }}
	 */
	verify(dateOfBirth, referenceDate = new Date()) {
		if (!dateOfBirth) {
			return {
				allowed: false,
				age: 0,
				reason: 'Date of birth is required by Meta API terms of service (18+ requirement).'
			};
		}
		const age = calculateAge(dateOfBirth, referenceDate);
		const allowed = age >= this.minAge;
		return {
			allowed,
			age,
			reason: allowed ? undefined : `User age (${age}) is under the minimum age requirement of ${this.minAge}.`
		};
	}
}

/**
 * 2. Geo & Location Policy Seam
 *
 * TODO (Captain Decision Pending):
 * Meta image generation (muse-image-1.0) availability in India and regional access restrictions:
 * - Direct IP-based geo-detection vs user-selected country profile
 * - Blocking vs disclaimers for image generation endpoints in affected regions
 *
 * Current Safe Default:
 * Configurable policy allowing all regions by default, with pluggable block list / warnings.
 */
export class DefaultLocationPolicy {
	constructor(options = {}) {
		// By default, no regions are blocked unless configured.
		this.blockedRegions = new Set(options.blockedRegions || []);
		this.restrictedImageRegions = new Set(options.restrictedImageRegions || []);
	}

	canAccessService(countryCode) {
		if (!countryCode) return { allowed: true };
		const code = countryCode.toUpperCase();
		if (this.blockedRegions.has(code)) {
			return {
				allowed: false,
				reason: `Service is not available in country code ${code}.`
			};
		}
		return { allowed: true };
	}

	canGenerateImages(countryCode) {
		if (!countryCode) return { allowed: true };
		const code = countryCode.toUpperCase();
		if (this.restrictedImageRegions.has(code)) {
			return {
				allowed: false,
				reason: `Image generation is currently restricted for region ${code} pending captain determination.`
			};
		}
		return { allowed: true };
	}
}

/**
 * 3. Image Hosting Policy Seam
 *
 * TODO (Captain Decision Pending):
 * Final object storage / CDN provider for generated comic images and export assets:
 * - S3 / Cloudflare R2 / GCS / Local volume
 * - Signed URLs vs public CDN distribution
 *
 * Current Safe Default:
 * Resolves URLs relative to a configured base storage URL, or returns static fake URLs in dev/test.
 */
export class DefaultImageHostingPolicy {
	constructor(baseUrl = '') {
		this.baseUrl = baseUrl.replace(/\/+$/, '');
	}

	resolveImageUrl(pathOrUrl) {
		if (!pathOrUrl) return '';
		if (pathOrUrl.startsWith('http://') || pathOrUrl.startsWith('https://') || pathOrUrl.startsWith('data:')) {
			return pathOrUrl;
		}
		const cleanPath = pathOrUrl.startsWith('/') ? pathOrUrl : `/${pathOrUrl}`;
		return this.baseUrl ? `${this.baseUrl}${cleanPath}` : cleanPath;
	}
}

// Default policy instances
export const defaultAgeGatePolicy = new DefaultAgeGatePolicy();
export const defaultLocationPolicy = new DefaultLocationPolicy();
export const defaultImageHostingPolicy = new DefaultImageHostingPolicy();
