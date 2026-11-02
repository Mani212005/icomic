/**
 * Icomic Go API Client.
 *
 * Implements the full HTTP API contract specified in docs/SPEC.md Section 10.
 * Sits behind an interface with a mock implementation used by tests and dev.
 */

export class ApiClient {
	constructor(baseUrl = 'http://localhost:8080') {
		this.baseUrl = baseUrl.replace(/\/+$/, '');
	}

	async request(path, options = {}) {
		const url = `${this.baseUrl}${path}`;
		const headers = {
			'Content-Type': 'application/json',
			...(options.headers || {})
		};

		const res = await fetch(url, {
			...options,
			headers
		});

		if (!res.ok) {
			let errorMessage = `API error ${res.status}`;
			try {
				const errorJson = await res.json();
				if (errorJson && (errorJson.message || errorJson.error)) {
					errorMessage = errorJson.message || errorJson.error;
				}
			} catch {
				// Not JSON, ignore
			}
			const error = new Error(errorMessage);
			error.status = res.status;
			throw error;
		}

		// Check for empty body or return json
		if (res.status === 204) return null;
		if (typeof res.json === 'function') {
			try {
				return await res.json();
			} catch {
				return res;
			}
		}
		return res;
	}

	// Health
	async checkHealth() {
		return this.request('/healthz');
	}

	// Projects
	async createProject(project) {
		return this.request('/api/v1/projects', {
			method: 'POST',
			body: JSON.stringify(project)
		});
	}

	async listProjects() {
		return this.request('/api/v1/projects');
	}

	async getProject(id) {
		return this.request(`/api/v1/projects/${id}`);
	}

	async updateProject(id, data) {
		return this.request(`/api/v1/projects/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
	}

	async deleteProject(id) {
		return this.request(`/api/v1/projects/${id}`, {
			method: 'DELETE'
		});
	}

	// Story Generation & Analysis
	async analyzeScript(projectId, scriptText) {
		return this.request(`/api/v1/projects/${projectId}/analyze`, {
			method: 'POST',
			body: JSON.stringify({ script: scriptText })
		});
	}

	// Characters
	async getCharacters(projectId) {
		return this.request(`/api/v1/projects/${projectId}/characters`);
	}

	async updateCharacter(id, data) {
		return this.request(`/api/v1/characters/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
	}

	async generateCharacterReference(id) {
		return this.request(`/api/v1/characters/${id}/reference-image`, {
			method: 'POST'
		});
	}

	async approveCharacter(id) {
		return this.request(`/api/v1/characters/${id}/approve`, {
			method: 'POST'
		});
	}

	// Chapters
	async createChapters(projectId, data) {
		return this.request(`/api/v1/projects/${projectId}/chapters`, {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async getChapters(projectId) {
		return this.request(`/api/v1/projects/${projectId}/chapters`);
	}

	async updateChapter(id, data) {
		return this.request(`/api/v1/chapters/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
	}

	async approveChapter(id) {
		return this.request(`/api/v1/chapters/${id}/approve`, {
			method: 'POST'
		});
	}

	// Panels
	async getPanels(chapterId) {
		return this.request(`/api/v1/chapters/${chapterId}/panels`);
	}

	async createPanels(chapterId, data) {
		return this.request(`/api/v1/chapters/${chapterId}/panels`, {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async updatePanel(id, data) {
		return this.request(`/api/v1/panels/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
	}

	// Pages
	async getPages(chapterId) {
		return this.request(`/api/v1/chapters/${chapterId}/pages`);
	}

	async updatePage(id, data) {
		return this.request(`/api/v1/pages/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
	}

	// Draft Stage
	async generateDraft(pageId) {
		return this.request(`/api/v1/pages/${pageId}/draft`, {
			method: 'POST'
		});
	}

	async approveDraft(pageId) {
		return this.request(`/api/v1/pages/${pageId}/draft/approve`, {
			method: 'POST'
		});
	}

	// Line Art Stage
	async generateLineArt(pageId) {
		return this.request(`/api/v1/pages/${pageId}/line-art`, {
			method: 'POST'
		});
	}

	async approveLineArt(pageId) {
		return this.request(`/api/v1/pages/${pageId}/line-art/approve`, {
			method: 'POST'
		});
	}

	// Colouring Stage
	async getColourPalette(pageId) {
		return this.request(`/api/v1/pages/${pageId}/colour-palette`);
	}

	async suggestColourPalette(pageId) {
		return this.request(`/api/v1/pages/${pageId}/colour-palette/suggest`, {
			method: 'POST'
		});
	}

	async updateColourPalette(pageId, palette) {
		return this.request(`/api/v1/pages/${pageId}/colour-palette`, {
			method: 'PATCH',
			body: JSON.stringify({ palette })
		});
	}

	async approveColour(pageId) {
		return this.request(`/api/v1/pages/${pageId}/colour/approve`, {
			method: 'POST'
		});
	}

	// Jobs and Credits
	async estimateCost(projectId, stage, imageCount = 1) {
		return this.request(`/api/v1/projects/${projectId}/estimate`, {
			method: 'POST',
			body: JSON.stringify({ stage, image_count: imageCount })
		});
	}

	async getJob(jobId) {
		return this.request(`/api/v1/generation-jobs/${jobId}`);
	}

	async cancelJob(jobId) {
		return this.request(`/api/v1/generation-jobs/${jobId}/cancel`, {
			method: 'POST'
		});
	}

	async getCreditUsage() {
		return this.request('/api/v1/me/credit-usage');
	}

	// Export
	async exportProject(projectId, options = {}) {
		return this.request(`/api/v1/projects/${projectId}/export`, {
			method: 'POST',
			body: JSON.stringify(options)
		});
	}

	async getExport(exportId) {
		return this.request(`/api/v1/exports/${exportId}`);
	}

	getExportDownloadUrl(exportId) {
		return `${this.baseUrl}/api/v1/exports/${exportId}/download`;
	}
}

export const api = new ApiClient();
