
import { JOB_STATUS, STAGE_ESTIMATED_COSTS } from './types.js';

let counter = 1;
function genId(prefix = 'id') {
	return `${prefix}-${counter++}`;
}

export class MockApiClient {
	constructor(options = {}) {
		this.baseUrl = 'mock://icomic.local';
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
	}

	isAuthenticated() {
		return true;
	}

	getAuthMode() {
		return 'mock';
	}

	seedInitialData() {
		const projId = 'proj-demo-1';
		const now = new Date().toISOString();

		this.projects.set(projId, {
			id: projId,
			title: 'The Quantum Detective',
			art_style: 'cyberpunk-noir',
			chapter_count: 3,
			is_sample: true,
			sample_label: 'Sample',
			sample_source: 'Meta AI',
			script: 'In the neon-drenched alleys of Sector 4, Detective Jack Vance pursued the enigmatic cipher...',
			original_script: 'In the neon-drenched alleys of Sector 4, Detective Jack Vance pursued the enigmatic cipher...',
			created_at: now,
			updated_at: now
		});

		const char1 = {
			id: 'char-1',
			project_id: projId,
			name: 'Jack Vance',
			description: 'Cynical cybernetic investigator',
			age_range: '35-45',
			appearance: 'Tall with worn trenchcoat and cybernetic left eye',
			hair_style: 'Disheveled dark hair',
			clothing: 'Weathered grey trenchcoat, dark turtleneck',
			personality: 'World-weary, observant, relentlessly determined',
			reference_image_url:
				'/mock/sample-character.webp',
			is_sample: true,
			sample_label: 'Sample',
			approved_at: now,
			created_at: now,
			updated_at: now
		};
		this.characters.set(char1.id, char1);

		const chap1 = {
			id: 'chap-1',
			project_id: projId,
			chapter_number: 1,
			title: 'Midnight in Sector 4',
			summary: 'Jack Vance discovers a murdered informant in a rainy neon alley.',
			approved_at: now,
			status: 'approved',
			created_at: now,
			updated_at: now
		};
		this.chapters.set(chap1.id, chap1);

		const panel1 = {
			id: 'panel-1',
			chapter_id: chap1.id,
			panel_number: 1,
			scene_description: 'Rain poured over towering holograms as Vance walked.',
			dialogue: 'Another night, another dead lead.',
			narration: 'The city breathes in neon and exhales static.',
			image_prompt: 'Cyberpunk noir detective walking in rain under holographic billboards',
			created_at: now,
			updated_at: now
		};
		this.panels.set(panel1.id, panel1);

		const page1 = {
			id: 'page-1',
			chapter_id: chap1.id,
			page_number: 1,
			panel_ids: [panel1.id],
			draft_image_url:
				'/mock/sample-draft.webp',
			draft_approved_at: now,
			line_art_url:
				'/mock/sample-line-art.webp',
			line_art_image_url:
				'/mock/sample-line-art.webp',
			is_sample: true,
			sample_label: 'Sample',
			sample_source: 'Meta AI',
			line_art_approved_at: now,
			colour_approved_at: null,
			created_at: now,
			updated_at: now
		};
		this.pages.set(page1.id, page1);

		this.palettes.set(page1.id, [
			{ region_name: 'sky', hex_color: '#1E293B', label: 'Night Sky' },
			{ region_name: 'neon', hex_color: '#38BDF8', label: 'Cyan Neon' },
			{ region_name: 'coat', hex_color: '#991B1B', label: 'Trenchcoat Crimson' },
			{ region_name: 'skin', hex_color: '#FDBA74', label: 'Warm Skin' },
			{ region_name: 'alley', hex_color: '#475569', label: 'Wet Concrete' }
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
			created_at: now,
			updated_at: now
		};
		this.projects.set(id, newProj);
		return newProj;
	}

	async listProjects() {
		return Array.from(this.projects.values());
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
		const now = new Date().toISOString();
		char.approved_at = now;
		char.updated_at = now;
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
		const now = new Date().toISOString();
		chap.approved_at = now;
		chap.status = 'approved';
		chap.updated_at = now;
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
		page.updated_at = now;
		return { job, page };
	}

	async approveDraft(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		const now = new Date().toISOString();
		page.draft_approved_at = now;
		page.updated_at = now;
		return page;
	}

	async generateLineArt(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		const job = this.createJob('proj-demo-1', 'final_line_art', 'line_art');
		const now = new Date().toISOString();
		page.line_art_url = '/mock/sample-line-art.webp';
		page.line_art_image_url = page.line_art_url;
		page.is_sample = true;
		page.sample_label = 'Sample';
		page.sample_source = 'Meta AI';
		page.line_art_approved_at = null;
		page.updated_at = now;
		return { job, page };
	}

	async approveLineArt(pageId) {
		const page = this.pages.get(pageId);
		if (!page) throw new Error(`Page ${pageId} not found`);
		const now = new Date().toISOString();
		page.line_art_approved_at = now;
		page.updated_at = now;
		return page;
	}

	async getColourPalette(pageId) {
		return this.palettes.get(pageId) || [];
	}

	async suggestColourPalette(pageId) {
		const job = this.createJob('proj-demo-1', 'colour_suggestion', 'colour');
		const palette = [
			{ region_name: 'sky', hex_color: '#1E293B', label: 'Night Sky' },
			{ region_name: 'neon', hex_color: '#38BDF8', label: 'Cyan Glow' },
			{ region_name: 'coat', hex_color: '#E11D48', label: 'Crimson Trenchcoat' },
			{ region_name: 'skin', hex_color: '#FED7AA', label: 'Character Skin' }
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
		const now = new Date().toISOString();
		page.colour_approved_at = now;
		page.updated_at = now;
		return page;
	}

	async estimateCost(projectId, stage, imageCount = 1) {
		const unitCost = STAGE_ESTIMATED_COSTS[stage] || 0.01;
		const estimatedCost = unitCost * imageCount;
		return {
			stage,
			image_count: imageCount,
			estimated_cost_usd: estimatedCost,
			current_balance_usd: this.creditUsage.remaining_credits
		};
	}

	async getJob(jobId) {
		const job = this.jobs.get(jobId);
		if (!job) throw new Error(`Job ${jobId} not found`);
		return job;
	}

	async cancelJob(jobId) {
		const job = this.jobs.get(jobId);
		if (!job) throw new Error(`Job ${jobId} not found`);
		job.status = JOB_STATUS.CANCELLED;
		return { status: 'cancelled', job_id: jobId };
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
			file_url: `mock://storage/export-${expId}.pdf`,
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
}

export const mockApi = new MockApiClient();
