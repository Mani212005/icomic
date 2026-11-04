
export const AGE_TERMS_ADVISORY = '18+ required by Meta API terms.';

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
