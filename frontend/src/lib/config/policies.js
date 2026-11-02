/**
 * Configuration and policy seams for Icomic.
 *
 * OWNER DECISIONS 2026-11-02:
 * - NO geo-block and NO region restriction anywhere (users are global).
 * - The 18+ gate is DEFERRED: do not build an enforced age gate; remove enforced age logic
 *   (age calculation, underage errors, date-of-birth based blocking). Advisory note only.
 * - Image hosting: resolves assets with pluggable base storage / CDN URL.
 */

/**
 * Advisory notice for user terms (18+ requirement from Meta API terms).
 * Enforced age gate is deferred; this is an informational advisory notice only.
 */
export const AGE_TERMS_ADVISORY = 'Users must be 18 years of age or older pursuant to Meta API terms of service.';

/**
 * Image Hosting Policy Seam
 * Resolves URLs relative to a configured base storage URL, or returns URLs directly.
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

export const defaultImageHostingPolicy = new DefaultImageHostingPolicy();
