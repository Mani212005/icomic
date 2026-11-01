/**
 * Icomic Go API Client.
 */

export class ApiClient {
	constructor(baseUrl = 'http://localhost:8080') {
		this.baseUrl = baseUrl;
	}

	async checkHealth() {
		const res = await fetch(`${this.baseUrl}/healthz`);
		if (!res.ok) {
			throw new Error(`API error ${res.status}`);
		}
		return res.json();
	}
}

export const api = new ApiClient();
