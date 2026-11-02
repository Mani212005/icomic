/**
 * Icomic Go API Client.
 *
 * Implements the full HTTP API contract specified in docs/SPEC.md Section 10 and ARCHITECTURE.md.
 * Connects to the Go API backend at PUBLIC_API_URL (default: http://localhost:8080).
 *
 * Identity Handling (SPEC 8.3, ARCHITECTURE 2, 8.3):
 * - If dev mode is configured (PUBLIC_DEV_MODE=true or devUserId provided), sends X-User-ID header.
 * - If a bearer token is provided, sends Authorization: Bearer <token>.
 * - If neither is configured, NEVER sends identity headers and exposes unauthenticated state.
 */

export class ApiClient {
	/**
	 * @param {string|{ baseUrl?: string, devMode?: boolean, devUserId?: string|null, token?: string|null }} [options]
	 */
	constructor(options = {}) {
		if (typeof options === 'string') {
			this.baseUrl = options.replace(/\/+$/, '');
			this.devMode = false;
			this.devUserId = null;
			this.token = null;
		} else {
			const envApiUrl =
				(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_API_URL) ||
				(typeof process !== 'undefined' && process.env?.PUBLIC_API_URL) ||
				'http://localhost:8080';

			const envDevMode =
				(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_DEV_MODE === 'true') ||
				(typeof process !== 'undefined' && process.env?.PUBLIC_DEV_MODE === 'true') ||
				false;

			const envDevUserId =
				(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_DEV_USER_ID) ||
				(typeof process !== 'undefined' && process.env?.PUBLIC_DEV_USER_ID) ||
				null;

			const envToken =
				(typeof import.meta !== 'undefined' && import.meta.env?.PUBLIC_AUTH_TOKEN) ||
				(typeof process !== 'undefined' && process.env?.PUBLIC_AUTH_TOKEN) ||
				null;

			this.baseUrl = (options.baseUrl || envApiUrl).replace(/\/+$/, '');
			this.devMode = options.devMode !== undefined ? options.devMode : (envDevMode || Boolean(envDevUserId));
			this.devUserId =
				options.devUserId !== undefined
					? options.devUserId
					: this.devMode
						? (envDevUserId || '00000000-0000-0000-0000-000000000001')
						: null;
			this.token = options.token !== undefined ? options.token : envToken;
		}
	}

	/**
	 * Reports whether the client has active credentials (dev mode identity or bearer token).
	 * @returns {boolean}
	 */
	isAuthenticated() {
		return Boolean((this.devMode && this.devUserId) || this.token);
	}

	/**
	 * Returns current authentication mode.
	 * @returns {'bearer'|'dev'|'unauthenticated'}
	 */
	getAuthMode() {
		if (this.token) return 'bearer';
		if (this.devMode && this.devUserId) return 'dev';
		return 'unauthenticated';
	}

	/**
	 * Configures or toggles local dev identity mode.
	 * @param {boolean} enabled
	 * @param {string} [userId='00000000-0000-0000-0000-000000000001']
	 */
	setDevMode(enabled, userId = '00000000-0000-0000-0000-000000000001') {
		this.devMode = Boolean(enabled);
		this.devUserId = enabled ? (userId || this.devUserId || '00000000-0000-0000-0000-000000000001') : null;
	}

	/**
	 * Configures bearer token authentication.
	 * @param {string|null} token
	 */
	setToken(token) {
		this.token = token || null;
	}

	async request(path, options = {}) {
		const url = `${this.baseUrl}${path}`;
		const headers = {
			'Content-Type': 'application/json',
			...(options.headers || {})
		};

		// Identity handling matching Go API auth seam (SPEC 8.3, ARCHITECTURE 2, 8.3):
		// 1. If dev mode is explicitly configured, send X-User-ID header.
		// 2. If bearer token is present, send Authorization: Bearer <token>.
		// 3. Otherwise NEVER send identity headers.
		if (this.devMode && this.devUserId && !headers['X-User-ID']) {
			headers['X-User-ID'] = this.devUserId;
		}
		if (this.token && !headers['Authorization']) {
			headers['Authorization'] = `Bearer ${this.token}`;
		}

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
		const payload = { ...project };
		if (payload.script && !payload.original_script) {
			payload.original_script = payload.script;
		}
		const res = await this.request('/api/v1/projects', {
			method: 'POST',
			body: JSON.stringify(payload)
		});
		return res && res.project ? res.project : res;
	}

	async listProjects() {
		const res = await this.request('/api/v1/projects');
		return res && res.projects ? res.projects : res;
	}

	async getProject(id) {
		const res = await this.request(`/api/v1/projects/${id}`);
		return res && res.project ? res.project : res;
	}

	async updateProject(id, data) {
		const payload = { ...data };
		if (payload.script && !payload.original_script) {
			payload.original_script = payload.script;
		}
		const res = await this.request(`/api/v1/projects/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(payload)
		});
		return res && res.project ? res.project : res;
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
		const res = await this.request(`/api/v1/projects/${projectId}/characters`);
		return res && res.characters ? res.characters : res;
	}

	async updateCharacter(id, data) {
		const res = await this.request(`/api/v1/characters/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
		return res && res.character ? res.character : res;
	}

	async generateCharacterReference(id) {
		return this.request(`/api/v1/characters/${id}/reference-image`, {
			method: 'POST'
		});
	}

	async approveCharacter(id) {
		const res = await this.request(`/api/v1/characters/${id}/approve`, {
			method: 'POST'
		});
		return res && res.character ? res.character : res;
	}

	// Chapters
	async createChapters(projectId, data) {
		return this.request(`/api/v1/projects/${projectId}/chapters`, {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async getChapters(projectId) {
		const res = await this.request(`/api/v1/projects/${projectId}/chapters`);
		return res && res.chapters ? res.chapters : res;
	}

	async updateChapter(id, data) {
		const res = await this.request(`/api/v1/chapters/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
		return res && res.chapter ? res.chapter : res;
	}

	async approveChapter(id) {
		const res = await this.request(`/api/v1/chapters/${id}/approve`, {
			method: 'POST'
		});
		return res && res.chapter ? res.chapter : res;
	}

	// Panels
	async getPanels(chapterId) {
		const res = await this.request(`/api/v1/chapters/${chapterId}/panels`);
		return res && res.panels ? res.panels : res;
	}

	async createPanels(chapterId, data) {
		return this.request(`/api/v1/chapters/${chapterId}/panels`, {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async updatePanel(id, data) {
		const res = await this.request(`/api/v1/panels/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
		return res && res.panel ? res.panel : res;
	}

	// Pages
	async getPages(chapterId) {
		const res = await this.request(`/api/v1/chapters/${chapterId}/pages`);
		return res && res.pages ? res.pages : res;
	}

	async updatePage(id, data) {
		const res = await this.request(`/api/v1/pages/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
		return res && res.page ? res.page : res;
	}

	// Draft Stage
	async generateDraft(pageId) {
		return this.request(`/api/v1/pages/${pageId}/draft`, {
			method: 'POST'
		});
	}

	async approveDraft(pageId) {
		const res = await this.request(`/api/v1/pages/${pageId}/draft/approve`, {
			method: 'POST'
		});
		return res && res.page ? res.page : res;
	}

	// Line Art Stage
	async generateLineArt(pageId) {
		return this.request(`/api/v1/pages/${pageId}/line-art`, {
			method: 'POST'
		});
	}

	async approveLineArt(pageId) {
		const res = await this.request(`/api/v1/pages/${pageId}/line-art/approve`, {
			method: 'POST'
		});
		return res && res.page ? res.page : res;
	}

	// Colouring Stage
	async getColourPalette(pageId) {
		const res = await this.request(`/api/v1/pages/${pageId}/colour-palette`);
		return res && res.palette ? res.palette : res;
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
		const res = await this.request(`/api/v1/pages/${pageId}/colour/approve`, {
			method: 'POST'
		});
		return res && res.page ? res.page : res;
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
		const res = await this.request('/api/v1/me/credit-usage');
		if (res && res.balance !== undefined && res.remaining_credits === undefined) {
			res.remaining_credits = res.balance;
		}
		return res;
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
