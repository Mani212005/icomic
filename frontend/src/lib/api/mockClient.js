
import { mockLettering } from './mockLettering.js';
import { JOB_STATUS, STAGE_ESTIMATED_COSTS } from './types.js';

let counter = 1;
function genId(prefix = 'id') {
	return `${prefix}-${counter++}`;
}

function apiError(message, code, status, details) {
	return Object.assign(new Error(message), { code, status, details });
}

export class MockApiClient {
	constructor(options = {}) {
		this.baseUrl = 'mock://icomic.local';
		// Fake in-memory accounts, enabled for the browser demo. Off by default so
		// tests that create many projects keep working.
		this.accountsEnabled = options.accounts ?? false;
		this.registry = new Map();
		this.owner = null;
		this.startNewGuest();
		this.autoCompleteJobs = options.autoCompleteJobs ?? true;
		this.failureRate = options.failureRate ?? 0;

		this.projects = new Map();
		this.characters = new Map();
		this.chapters = new Map();
		this.panels = new Map();
		this.pages = new Map();
		this.jobs = new Map();
		this.palettes = new Map();
		this.exports = new Map();

		this.creditUsage = {
			total_credits: 1000,
			used_credits: 14.5,
			remaining_credits: 985.5,
			currency: 'USD'
		};

		this.seedInitialData();
		this.letteringBases = new Map();
		this.letteringTexts = new Map();
		for (const { page, lettering } of options.letteringFixtures || []) {
			this.pages.set(page.id, structuredClone(page));
			this.letteringBases.set(page.id, structuredClone(lettering));
			this.pages.get(page.id).lettering = structuredClone(lettering);
		}
	}

	isAuthenticated() {
		return true;
	}

	startNewGuest() {
		this.owner = `guest:${counter++}`;
		this.email = null;
	}

	ownProjects() {
		return Array.from(this.projects.values()).filter((p) => p.owner === this.owner);
	}

	accountView() {
		const count = this.ownProjects().length;
		if (!this.accountsEnabled) {
			return { kind: 'registered', email: null, project_count: count, project_limit: null };
		}
		const registered = Boolean(this.email);
		return {
			kind: registered ? 'registered' : 'guest',
			email: this.email,
			project_count: count,
			project_limit: registered ? 100 : 1
		};
	}

	async getAccount() {
		return this.accountView();
	}

	async startGuest() {
		return this.accountView();
	}

	async ensureSession() {
		return this.accountView();
	}

	validateCredentials(email, password) {
		const normalized = String(email || '').trim().toLowerCase();
		if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(normalized)) {
			throw apiError('Enter a valid email address.', 'invalid_email', 400);
		}
		if (String(password || '').length < 10) {
			throw apiError('Use at least 10 characters.', 'weak_password', 400);
		}
		return normalized;
	}

	adopt(email, claimFrom) {
		let claimed = 0;
		const key = `user:${email}`;
		for (const p of this.projects.values()) {
			if (claimFrom && p.owner === claimFrom) {
				p.owner = key;
				claimed += 1;
			}
		}
		this.owner = key;
		this.email = email;
		return { account: this.accountView(), ...(claimed ? { claimed_projects: claimed } : {}) };
	}

	async signUp(email, password) {
		const normalized = this.validateCredentials(email, password);
		if (this.registry.has(normalized)) {
			throw apiError('An account with this email already exists.', 'email_taken', 409);
		}
		this.registry.set(normalized, password);
		return this.adopt(normalized, this.email ? null : this.owner);
	}

	async signIn(email, password) {
		const normalized = String(email || '').trim().toLowerCase();
		if (this.registry.get(normalized) !== password || !password) {
			throw apiError('Incorrect email or password.', 'invalid_credentials', 401);
		}
		return this.adopt(normalized, this.email ? null : this.owner);
	}

	async signOut() {
		this.startNewGuest();
	}

	getAuthMode() {
		return 'mock';
	}

	seedInitialData() {
		const projId = 'proj-demo-1';
		const now = new Date().toISOString();
		const approvedAt = new Date(Date.parse(now) + 1).toISOString();

		this.projects.set(projId, {
			id: projId,
			title: 'The Lighthouse Bird',
			art_style: 'indie-graphic-novel',
			chapter_count: 1,
			is_sample: true,
			owner: null,
			sample_label: 'Sample',
			sample_source: 'Meta AI',
			script: 'Nia, a young lighthouse keeper, discovers a tiny brass bird during a storm. The bird lights the dark sea, guiding a lost boat safely home. At sunrise, Nia sets it free and watches it fly toward the bright horizon.',
			original_script: 'Nia, a young lighthouse keeper, discovers a tiny brass bird during a storm. The bird lights the dark sea, guiding a lost boat safely home. At sunrise, Nia sets it free and watches it fly toward the bright horizon.',
			created_at: now,
			updated_at: now
		});

		const char1 = {
			id: 'char-1',
			project_id: projId,
			name: 'Nia',
			description: 'A young lighthouse keeper who finds a tiny brass bird during a storm',
			age_range: '8-12',
			appearance: 'Dark curly hair, freckles, warm brown skin, expressive curious eyes',
			hair_style: 'Windblown dark curls tied back',
			clothing: 'Navy lighthouse keeper coat over a cream sweater',
			personality: 'Curious, brave, gentle, and caring',
			reference_image_url:
				'/mock/sample-character.webp',
			is_sample: true,
			sample_label: 'Sample',
			approved_at: approvedAt,
			created_at: now,
			updated_at: now
		};
		this.characters.set(char1.id, char1);

		const chap1 = {
			id: 'chap-1',
			project_id: projId,
			chapter_number: 1,
			title: 'The Lighthouse Bird',
			summary: 'Nia finds a tiny brass bird in a storm; its light guides a lost boat home, and she frees it at sunrise.',
			approved_at: approvedAt,
			status: 'approved',
			created_at: now,
			updated_at: now
		};
		this.chapters.set(chap1.id, chap1);

		const panelBeats = [
			{ scene_description: 'During a storm, Nia discovers a tiny brass bird inside the lighthouse.', dialogue: 'Nia: Oh! What are you, little one?', narration: 'Rain lashes the lighthouse as a golden light flickers to life.', image_prompt: 'Nia discovers a tiny glowing brass bird in a storm-lit lighthouse.' },
			{ scene_description: 'The brass bird shines brighter than the lighthouse lamp over the dark sea.', dialogue: 'Nia: Your light is brighter than the lamp!', narration: 'The bird sends a warm beam over the rough waves.', image_prompt: 'Nia holds the shining brass bird above the stormy sea.' },
			{ scene_description: 'The bird guides a lost boat safely toward the lighthouse.', dialogue: 'Sailor: We see it! Steer home!', narration: 'A lost boat follows the bird\'s golden light through the storm.', image_prompt: 'The bird guides a small boat through dark waves toward safety.' },
			{ scene_description: 'At sunrise, Nia sets the bird free and watches it fly toward the bright horizon.', dialogue: 'Nia: Fly free, little light.', narration: 'The storm clears as the brass bird rises into the sunrise.', image_prompt: 'Nia releases the tiny brass bird into a peach-colored sunrise.' }
		];
		const seededPanels = panelBeats.map((beat, index) => ({
			id: `panel-${index + 1}`,
			chapter_id: chap1.id,
			panel_number: index + 1,
			...beat,
			created_at: now,
			updated_at: now
		}));
		seededPanels.forEach((panel) => this.panels.set(panel.id, panel));

		const page1 = {
			id: 'page-1',
			lettering_mode: 'baked',
			chapter_id: chap1.id,
			page_number: 1,
			panel_ids: seededPanels.map((panel) => panel.id),
			draft_image_url:
				'/mock/sample-draft.webp',
			draft_approved_at: approvedAt,
			line_art_url:
				'/mock/sample-line-art.webp',
			line_art_image_url:
				'/mock/sample-line-art.webp',
			is_sample: true,
			sample_label: 'Sample',
			sample_source: 'Meta AI',
			line_art_approved_at: approvedAt,
			colour_approved_at: null,
			created_at: now,
			updated_at: now
		};
		this.pages.set(page1.id, page1);

		this.palettes.set(page1.id, [
			{ region_name: 'sea', hex_color: '#23415A', label: 'Stormy Sea' },
			{ region_name: 'coat', hex_color: '#24364B', label: 'Nia’s Navy Coat' },
			{ region_name: 'bird', hex_color: '#DCA83D', label: 'Brass Bird' },
			{ region_name: 'lamp', hex_color: '#F6C46B', label: 'Lantern Glow' },
			{ region_name: 'sky', hex_color: '#E9A77E', label: 'Sunrise Sky' }
		]);
	}

	createJob(projectId, jobType, stage) {
		const jobId = genId('job');
		const cost = STAGE_ESTIMATED_COSTS[jobType] || 0.01;
		const job = {
			id: jobId,
			project_id: projectId,
			user_id: 'usr-default-mock',
			job_type: jobType,
			stage,
			status: this.autoCompleteJobs ? JOB_STATUS.DONE : JOB_STATUS.PENDING,
			idempotency_key: `idem-${Date.now()}-${Math.random().toString(36).substring(2, 8)}`,
			estimated_cost: cost,
			actual_cost: this.autoCompleteJobs ? cost : null,
			hashed_user_id: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
			error_message: null,
			created_at: new Date().toISOString()
		};
		this.jobs.set(jobId, job);
		return job;
	}

	async checkHealth() {
		return { status: 'ok', service: 'mock-icomic-api' };
	}

	async createProject(project) {
		const account = this.accountView();
		if (account.project_limit !== null && account.project_count >= account.project_limit) {
			throw apiError('Project limit reached.', 'project_limit_reached', 403, {
				account_kind: account.kind,
				limit: account.project_limit
			});
		}
		const id = genId('proj');
		const now = new Date().toISOString();
		const script = project.original_script || project.script || '';
		const newProj = {
			id,
			title: project.title || 'Untitled Comic',
			art_style: project.art_style || 'classic-comic',
			chapter_count: project.chapter_count || 1,
			script,
			original_script: script,
			owner: this.owner,
			created_at: now,
			updated_at: now
		};
		this.projects.set(id, newProj);
		return newProj;
	}

	async listProjects() {
		return Array.from(this.projects.values()).filter(
			(p) => !this.accountsEnabled || p.owner === null || p.owner === this.owner
		);
	}

	async getProject(id) {
		const p = this.projects.get(id);
		if (!p) throw new Error(`Project ${id} not found`);
		return p;
	}

	async updateProject(id, data) {
		const p = await this.getProject(id);
		const script = data.original_script || data.script || p.script;
		const updated = {
			...p,
			...data,
			script,
			original_script: script,
			updated_at: new Date().toISOString()
		};
		this.projects.set(id, updated);
		return updated;
	}

	async deleteProject(id) {
		this.projects.delete(id);
		return { success: true };
	}

	async analyzeScript(projectId, scriptText) {
		const p = await this.getProject(projectId);
		p.script = scriptText;
		p.original_script = scriptText;
		p.updated_at = new Date().toISOString();
		const job = this.createJob(projectId, 'script_analysis', 'script');
		return { job, project_id: projectId };
	}

	async getCharacters(projectId) {
		return Array.from(this.characters.values()).filter((c) => c.project_id === projectId);
	}

	async updateCharacter(id, data) {
		const char = this.characters.get(id);
		if (!char) throw new Error(`Character ${id} not found`);

		let now = new Date().toISOString();
		if (char.approved_at && new Date(char.approved_at).getTime() >= new Date(now).getTime()) {
			now = new Date(new Date(char.approved_at).getTime() + 1000).toISOString();
		}

		const updated = {
			...char,
			...data,
			updated_at: now
		};
		this.characters.set(id, updated);
		return updated;
	}

	async generateCharacterReference(id) {
		const char = this.characters.get(id);
		if (!char) throw new Error(`Character ${id} not found`);
		const job = this.createJob(char.project_id, 'character_reference', 'characters');
		char.reference_image_url = '/mock/sample-character.webp';
		char.is_sample = true;
		char.sample_label = 'Sample';

		let now = new Date().toISOString();
		if (char.approved_at && new Date(char.approved_at).getTime() >= new Date(now).getTime()) {
			now = new Date(new Date(char.approved_at).getTime() + 1000).toISOString();
		}
		char.updated_at = now;

		return { job, character: char };
	}

	async approveCharacter(id) {
		const char = this.characters.get(id);
		if (!char) throw new Error(`Character ${id} not found`);
		let now = Date.now();
		const activity = Date.parse(char.updated_at);
		if (Number.isFinite(activity) && now <= activity) now = activity + 1;
		char.approved_at = new Date(now).toISOString();
		return char;
	}

	async createChapters(projectId, data) {
		const count = data.chapter_count || 1;
		const created = [];
		const now = new Date().toISOString();
		for (let i = 1; i <= count; i++) {
			const chap = {
				id: genId('chap'),
				project_id: projectId,
				chapter_number: i,
				title: `Chapter ${i}`,
				summary: `Summary of events in chapter ${i}`,
				approved_at: null,
				status: 'draft',
				created_at: now,
				updated_at: now
			};
			this.chapters.set(chap.id, chap);
			created.push(chap);
		}
		return created;
	}

	async getChapters(projectId) {
		return Array.from(this.chapters.values()).filter((c) => c.project_id === projectId);
	}

	async updateChapter(id, data) {
		const chap = this.chapters.get(id);
		if (!chap) throw new Error(`Chapter ${id} not found`);

		let now = new Date().toISOString();
		if (chap.approved_at && new Date(chap.approved_at).getTime() >= new Date(now).getTime()) {
			now = new Date(new Date(chap.approved_at).getTime() + 1000).toISOString();
		}
		const updated = { ...chap, ...data, updated_at: now };
		this.chapters.set(id, updated);
		return updated;
	}

	async approveChapter(id) {
		const chap = this.chapters.get(id);
		if (!chap) throw new Error(`Chapter ${id} not found`);
		let now = Date.now();
		const activity = Date.parse(chap.updated_at);
		if (Number.isFinite(activity) && now <= activity) now = activity + 1;
		chap.approved_at = new Date(now).toISOString();
		chap.status = 'approved';
		return chap;
	}

	async getPanels(chapterId) {
		return Array.from(this.panels.values()).filter((p) => p.chapter_id === chapterId);
	}

	async createPanels(chapterId, data) {
		const panels = data.panels || [];
		const now = new Date().toISOString();
		const created = panels.map((p, idx) => {
			const panel = {
				id: genId('panel'),
				chapter_id: chapterId,
				panel_number: idx + 1,
				scene_description: p.scene_description || '',
				dialogue: p.dialogue || '',
				narration: p.narration || '',
				image_prompt: p.image_prompt || '',
				created_at: now,
				updated_at: now
			};
			this.panels.set(panel.id, panel);
			return panel;
		});
		return created;
	}

	async updatePanel(id, data) {
		const p = this.panels.get(id);
		if (!p) throw new Error(`Panel ${id} not found`);
		const now = new Date().toISOString();
		const updated = { ...p, ...data, updated_at: now };
		this.panels.set(id, updated);

		for (const pg of this.pages.values()) {
			if (pg.panel_ids && pg.panel_ids.includes(id)) {
				pg.draft_approved_at = null;
				pg.updated_at = now;
			}
		}
		return updated;
	}

	async getPages(chapterId) {
		return Array.from(this.pages.values()).filter((p) => p.chapter_id === chapterId);
	}

	async getLettering(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error('Page not found');
		if (page.lettering_mode !== 'overlay' || page.line_art_image_url || page.line_art_url) return { lettering_mode: 'baked' };
		if (!page.lettering) throw new Error('Layout unavailable');
		return structuredClone({ lettering_mode: 'overlay', lettering: page.lettering });
	}

	async patchLetteringOverride(pageId, utteranceId, override) {
		const response = await this.getLettering(pageId);
		if (!response.lettering) throw new Error('Baked art cannot receive lettering');
		const state = response.lettering;
		if (!state.input.panels.some((p) => p.utterances.some((u) => u.utterance_id === utteranceId))) throw new Error('Utterance not found');
		if (override.size_scale !== undefined && (!Number.isFinite(override.size_scale) || override.size_scale <= 0 || override.size_scale > 8)) throw new Error('Invalid size');
		state.overrides[utteranceId] = structuredClone(override);
		if (!Object.keys(override).length) delete state.overrides[utteranceId];
		this.pages.get(pageId).lettering = mockLettering(this.letteringBases.get(pageId), state.overrides, this.letteringTexts.get(pageId));
		return this.getLettering(pageId);
	}

	async patchLetteringText(pageId, utteranceId, text) {
		const response = await this.getLettering(pageId);
		if (!response.lettering) throw new Error('Baked art cannot receive lettering');
		const entry = response.lettering.input.panels.flatMap((p) => p.utterances).find((u) => u.utterance_id === utteranceId);
		if (!entry) throw new Error('Utterance not found');
		if (typeof text !== 'string' || text.length > 8000 || (entry.kind === 'speech' && /[\r\n]/.test(text))) throw new Error('Invalid text');
		const texts = { ...this.letteringTexts.get(pageId), [utteranceId]: text };
		this.letteringTexts.set(pageId, texts);
		this.pages.get(pageId).lettering = mockLettering(this.letteringBases.get(pageId), response.lettering.overrides, texts);
		return this.getLettering(pageId);
	}

	async updatePage(id, data) {
		const page = this.pages.get(id);
		if (!page) throw new Error(`Page ${id} not found`);
		const now = new Date().toISOString();
		const updated = { ...page, ...data, updated_at: now };
		this.pages.set(id, updated);
		return updated;
	}

	async generateDraft(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		const job = this.createJob('proj-demo-1', 'draft_sketch', 'draft');
		const now = new Date().toISOString();
		page.draft_image_url = '/mock/sample-draft.webp';
		page.is_sample = true;
		page.sample_label = 'Sample';

		page.draft_approved_at = null;
		page.line_art_approved_at = null;
		page.painted_page_approved_at = null;
		page.updated_at = now;
		return { job, page };
	}

	async approveDraft(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		let now = Date.now();
		const activity = Date.parse(page.updated_at);
		if (Number.isFinite(activity) && now <= activity) now = activity + 1;
		page.draft_approved_at = new Date(now).toISOString();
		return page;
	}

	async generateLineArt(pageId, variant) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		const job = this.createJob('proj-demo-1', 'final_line_art', 'line_art');
		const now = new Date().toISOString();
		if (variant === 'ink') {
			page.line_art_url = '/mock/sample-line-art.webp';
			page.line_art_image_url = page.line_art_url;
			page.line_art_approved_at = null;
		} else {
			page.painted_page_image_url = '/mock/sample-painted-page.webp';
			page.painted_page_variant = 'direct-from-brief';
			page.painted_page_approved_at = null;
		}
		page.is_sample = true;
		page.sample_label = 'Sample';
		page.sample_source = 'Meta AI';
		page.updated_at = now;
		return { job, page };
	}

	async approveLineArt(pageId, variant) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		let now = Date.now();
		const activity = Date.parse(page.updated_at);
		if (Number.isFinite(activity) && now <= activity) now = activity + 1;
		const approvedAt = new Date(now).toISOString();
		if (variant === 'ink') page.line_art_approved_at = approvedAt;
		else page.painted_page_approved_at = approvedAt;
		return page;
	}

	async getColourPalette(pageId) {
		return this.palettes.get(pageId) || [];
	}

	async suggestColourPalette(pageId) {
		const job = this.createJob('proj-demo-1', 'colour_suggestion', 'colour');
		const palette = [
			{ region_name: 'sky', hex_color: '#7DA0C4', label: 'Morning Sky' },
			{ region_name: 'foliage', hex_color: '#2D5A27', label: 'Forest Green' },
			{ region_name: 'cloak', hex_color: '#4A6B32', label: 'Forest Cloak' },
			{ region_name: 'skin', hex_color: '#EDBB99', label: 'Warm Skin' }
		];
		this.palettes.set(pageId, palette);
		return { job, palette };
	}

	async updateColourPalette(pageId, palette) {
		this.palettes.set(pageId, palette);
		return { palette };
	}

	async approveColour(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		let now = Date.now();
		const activity = Date.parse(page.updated_at);
		if (Number.isFinite(activity) && now <= activity) now = activity + 1;
		page.colour_approved_at = new Date(now).toISOString();
		return page;
	}

	async estimateCost(projectId, stage, imageCount = 1) {
		const unitCost = STAGE_ESTIMATED_COSTS[stage] || 0.01;
		const estimatedCost = unitCost * imageCount;
		return {
			stage_name: stage,
			image_count: stage === 'colour_suggestion' ? 0 : imageCount,
			estimated_cost: estimatedCost,
			assumptions: ['Mock configured estimate.'],
			is_estimate_price: stage === 'colour_suggestion',
			model_id: stage === 'colour_suggestion' ? 'muse-spark-1.3' : 'muse-image-1.0',
			currency: 'USD',
			pricing_version: 'mock-default',
			price_verified: false
		};
	}

	async getJob(jobId) {
		const job = this.jobs.get(jobId);
		if (!job) throw new Error(`Job ${jobId} not found`);
		return job;
	}

	// Mirrors the server: cancel is pending-only. A job that has not started is cancelled for good;
	// one already running is settling and keeps running; a finished one is returned as it is.
	async cancelJob(jobId) {
		const job = this.jobs.get(jobId);
		if (!job) throw new Error(`Job ${jobId} not found`);
		if (job.status === JOB_STATUS.PENDING) {
			job.status = JOB_STATUS.CANCELLED;
			return { outcome: 'cancelled', job: { ...job }, refunded: 0, message: 'Cancelled before it reached the model. Nothing was charged.' };
		}
		if (job.status === JOB_STATUS.RUNNING) {
			return { outcome: 'settling', job: { ...job }, refunded: 0, message: 'Already with the model. It will finish and you will be charged; the result is kept.' };
		}
		return { outcome: 'finished', job: { ...job }, refunded: 0, message: 'This job had already finished.' };
	}

	async getCreditUsage() {
		return this.creditUsage;
	}

	async exportProject(projectId, options = {}) {
		const expId = genId('exp');
		const job = this.createJob(projectId, 'pdf_assembly', 'export');
		const exp = {
			id: expId,
			project_id: projectId,
			format: options.format || 'pdf',
			page_size: options.page_size || 'a4',
			file_url: `mock://storage/export-${expId}.${options.format === 'png' ? 'zip' : 'pdf'}`,
			status: 'ready',
			created_at: new Date().toISOString()
		};
		this.exports.set(expId, exp);
		return { job, export: exp };
	}

	async getExport(exportId) {
		const exp = this.exports.get(exportId);
		if (!exp) throw new Error(`Export ${exportId} not found`);
		return exp;
	}

	getExportDownloadUrl(exportId) {
		return `mock://storage/export-${exportId}/download.pdf`;
	}

	async saveExport() {}
}

export const mockApi = new MockApiClient();
