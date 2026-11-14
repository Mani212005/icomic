
import { newIdempotencyKey } from './idempotency.js';

export class ApiClient {

	constructor(options = {}) {
		this.account = null;
		this.csrfToken = null;
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

	isAuthenticated() {
		return Boolean((this.devMode && this.devUserId) || this.token);
	}

	getAuthMode() {
		if (this.token) return 'bearer';
		if (this.devMode && this.devUserId) return 'dev';
		return 'unauthenticated';
	}

	setDevMode(enabled, userId = '00000000-0000-0000-0000-000000000001') {
		this.devMode = Boolean(enabled);
		this.devUserId = enabled ? (userId || this.devUserId || '00000000-0000-0000-0000-000000000001') : null;
	}

	setToken(token) {
		this.token = token || null;
	}

	authHeaders(method, headers = {}) {
		const out = { ...headers };
		if (this.devMode && this.devUserId && !out['X-User-ID']) {
			out['X-User-ID'] = this.devUserId;
		}
		if (this.token && !out['Authorization']) {
			out['Authorization'] = `Bearer ${this.token}`;
		}
		if (this.csrfToken && method !== 'GET' && !out['X-CSRF-Token']) {
			out['X-CSRF-Token'] = this.csrfToken;
		}
		return out;
	}

	async request(path, options = {}) {
		const url = `${this.baseUrl}${path}`;
		const method = (options.method || 'GET').toUpperCase();
		const headers = this.authHeaders(method, {
			'Content-Type': 'application/json',
			...(options.headers || {})
		});

		const res = await fetch(url, {
			...options,
			credentials: 'include',
			headers
		});

		if (!res.ok) {
			let errorMessage = `API error ${res.status}`;
			let errorCode;
			let retryable = false;
			let retryAfterSeconds;
			let details;
			try {
				const errorJson = await res.json();
				errorCode = errorJson?.code;
				details = errorJson?.details;
				retryable = errorJson?.retryable === true;
				retryAfterSeconds = errorJson?.retry_after_seconds;
				if (errorJson && (errorJson.message || errorJson.error)) {
					errorMessage = errorJson.message || errorJson.error;
				}
			} catch {

			}
			const error = new Error(errorMessage);
			error.status = res.status;
			error.code = errorCode;
			error.details = details;
			error.retryable = retryable;
			error.retry_after_seconds = retryAfterSeconds;
			throw error;
		}

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

	// Accounts: cookie session plus per-session CSRF token. The token lives in memory only.
	remember(res) {
		this.account = res?.account ?? null;
		this.csrfToken = this.account?.csrf_token ?? null;
		return res;
	}

	async getAccount() {
		return this.remember(await this.request('/api/v1/auth/me')).account ?? null;
	}

	async startGuest() {
		return this.remember(await this.request('/api/v1/auth/guest', { method: 'POST' })).account;
	}

	// A guest session is created lazily, when the visitor first needs one.
	async ensureSession() {
		return (await this.getAccount()) || this.startGuest();
	}

	async signUp(email, password) {
		const body = JSON.stringify({ email, password });
		return this.remember(await this.request('/api/v1/auth/signup', { method: 'POST', body }));
	}

	async signIn(email, password) {
		const body = JSON.stringify({ email, password });
		return this.remember(await this.request('/api/v1/auth/signin', { method: 'POST', body }));
	}

	async signOut() {
		await this.request('/api/v1/auth/signout', { method: 'POST' });
		this.remember(null);
	}

	async checkHealth() {
		return this.request('/healthz');
	}

	async createProject(project) {
		const payload = { ...project };
		if (payload.script && !payload.original_script) {
			payload.original_script = payload.script;
		}
		delete payload.script;
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
		delete payload.script;
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

	async analyzeScript(projectId, scriptText) {
		return this.request(`/api/v1/projects/${projectId}/analyze`, {
			method: 'POST',
			body: JSON.stringify({ script: scriptText })
		});
	}

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

	// A paid generation returns a receipt ({ job, job_id, status, entity_type, entity_id }) as soon as
	// the job and its charge are committed; the result is read from the job and the entity.
	// One key per user action makes a repeated request return the same job instead of a second one,
	// so a dropped connection is retried safely and never pays twice.
	async postGeneration(path, options = {}) {
		const key = options.idempotencyKey || newIdempotencyKey();
		const attempts = options.attempts ?? 3;
		for (let attempt = 1; ; attempt += 1) {
			try {
				return await this.request(path, { method: 'POST', headers: { 'Idempotency-Key': key } });
			} catch (err) {
				if (err?.status || !(err instanceof TypeError) || attempt >= attempts) throw err;
				await new Promise((resolve) => setTimeout(resolve, (options.retryDelayMs ?? 500) * attempt));
			}
		}
	}

	async generateCharacterReference(id, options) {
		return this.postGeneration(`/api/v1/characters/${id}/reference-image`, options);
	}

	async approveCharacter(id) {
		const res = await this.request(`/api/v1/characters/${id}/approve`, {
			method: 'POST'
		});
		return res && res.character ? res.character : res;
	}

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

	// Structural panel edits are chapter-scoped and atomic on the server: each stales the approvals of every
	// page it changes. addPanel puts the panel on data.page_id (at data.position) or on a new last page.
	async addPanel(chapterId, data) {
		return this.request(`/api/v1/chapters/${chapterId}/panels/add`, {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async movePanel(chapterId, panelId, pageId, position) {
		return this.request(`/api/v1/chapters/${chapterId}/panels/move`, {
			method: 'POST',
			body: JSON.stringify({ panel_id: panelId, page_id: pageId, ...(position === undefined ? {} : { position }) })
		});
	}

	async deletePanel(chapterId, panelId) {
		return this.request(`/api/v1/chapters/${chapterId}/panels/${panelId}`, { method: 'DELETE' });
	}

	async updatePanel(id, data) {
		const res = await this.request(`/api/v1/panels/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
		return res && res.panel ? res.panel : res;
	}

	async getPages(chapterId) {
		const res = await this.request(`/api/v1/chapters/${chapterId}/pages`);
		return res && res.pages ? res.pages : res;
	}

	async getLettering(pageId) {
		return this.request(`/api/v1/pages/${encodeURIComponent(pageId)}/lettering`);
	}

	async patchLetteringOverride(pageId, utteranceId, override) {
		return this.request(`/api/v1/pages/${encodeURIComponent(pageId)}/lettering/utterances/${encodeURIComponent(utteranceId)}/override`, {
			method: 'PATCH', body: JSON.stringify({ override })
		});
	}

	async patchLetteringText(pageId, utteranceId, text) {
		return this.request(`/api/v1/pages/${encodeURIComponent(pageId)}/lettering/utterances/${encodeURIComponent(utteranceId)}/text`, {
			method: 'PATCH', body: JSON.stringify({ text })
		});
	}

	async updatePage(id, data) {
		const res = await this.request(`/api/v1/pages/${id}`, {
			method: 'PATCH',
			body: JSON.stringify(data)
		});
		return res && res.page ? res.page : res;
	}

	async generateDraft(pageId, options) {
		return this.postGeneration(`/api/v1/pages/${pageId}/draft`, options);
	}

	async approveDraft(pageId) {
		const res = await this.request(`/api/v1/pages/${pageId}/draft/approve`, {
			method: 'POST'
		});
		return res && res.page ? res.page : res;
	}

	async generateLineArt(pageId, variant, options) {
		return this.postGeneration(`/api/v1/pages/${pageId}/line-art${variant === 'ink' ? '?variant=ink' : ''}`, options);
	}

	async approveLineArt(pageId, variant) {
		const res = await this.request(`/api/v1/pages/${pageId}/line-art/approve${variant === 'ink' ? '?variant=ink' : ''}`, {
			method: 'POST'
		});
		return res && res.page ? res.page : res;
	}

	async getColourPalette(pageId) {
		const res = await this.request(`/api/v1/pages/${pageId}/colour-palette`);
		return res && res.palette ? res.palette : res;
	}

	async suggestColourPalette(pageId, options) {
		return this.postGeneration(`/api/v1/pages/${pageId}/colour-palette/suggest`, options);
	}

	async updateColourPalette(pageId, palette) {
		return this.request(`/api/v1/pages/${pageId}/colour-palette`, {
			method: 'PATCH',
			body: JSON.stringify({ palette })
		});
	}

	async uploadColourImage(pageId, imageBlob) {
		return this.request(`/api/v1/pages/${pageId}/colour-image`, {
			method: 'PUT',
			body: imageBlob,
			headers: { 'Content-Type': imageBlob?.type || 'image/png' }
		});
	}

	async approveColour(pageId) {
		const res = await this.request(`/api/v1/pages/${pageId}/colour/approve`, {
			method: 'POST'
		});
		return res && res.page ? res.page : res;
	}

	async estimateCost(projectId, stage, imageCount = 1) {
		return this.request(`/api/v1/projects/${projectId}/estimate`, {
			method: 'POST',
			body: JSON.stringify({ stage, image_count: imageCount })
		});
	}

	async getJob(jobId) {
		const response = await this.request(`/api/v1/generation-jobs/${jobId}`);
		return response?.job || response;
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

	// A bare link cannot carry the bearer or dev identity, so the file is fetched with the same credentials.
	async downloadExport(exportId) {
		const res = await fetch(this.getExportDownloadUrl(exportId), {
			credentials: 'include',
			headers: this.authHeaders('GET')
		});
		if (!res.ok) {
			const error = new Error(`Download failed (${res.status})`);
			error.status = res.status;
			throw error;
		}
		return res.blob();
	}

	// Hands the authenticated download to the browser as a file.
	async saveExport(exportId, format) {
		const objectUrl = URL.createObjectURL(await this.downloadExport(exportId));
		const anchor = document.createElement('a');
		anchor.href = objectUrl;
		anchor.download = `comic.${format}`;
		anchor.click();
		URL.revokeObjectURL(objectUrl);
	}
}

export const api = new ApiClient();
