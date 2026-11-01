/**
 * Icomic Go API Client.
 */

export class ApiClient {
	constructor(baseUrl = 'http://localhost:8080') {
		this.baseUrl = baseUrl;
	}

	async request(path, options = {}) {
		const res = await fetch(`${this.baseUrl}${path}`, {
			headers: {
				'Content-Type': 'application/json',
				...(options.headers || {})
			},
			...options
		});
		if (!res.ok) {
			const text = await res.text();
			throw new Error(`API error ${res.status}: ${text}`);
		}
		return res.json();
	}

	async checkHealth() {
		return this.request('/healthz');
	}

	async createProject(project) {
		return this.request('/api/v1/projects', {
			method: 'POST',
			body: JSON.stringify(project)
		});
	}

	async listProjects() {
		return this.request('/api/v1/projects');
	}

	async estimateCost(projectId, stage, imageCount) {
		return this.request(`/api/v1/projects/${projectId}/estimate`, {
			method: 'POST',
			body: JSON.stringify({ stage, image_count: imageCount })
		});
	}

	async getCreditUsage() {
		return this.request('/api/v1/me/credit-usage');
	}
}

export const api = new ApiClient();
