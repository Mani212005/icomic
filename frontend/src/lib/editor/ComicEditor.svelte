<script>
	import ColourEditor from '$lib/colour/ColourEditor.svelte';
	import ExportView from '$lib/export/ExportView.svelte';
	import { pollJob } from '$lib/api/jobPolling.js';
	import { STAGES, STAGE_ESTIMATED_COSTS } from '$lib/api/types.js';

	export let project = {
		id: 'proj-demo-1',
		title: 'The Quantum Detective',
		art_style: 'cyberpunk-noir',
		chapter_count: 3,
		script: ''
	};
	export let apiClient;
	export let activeStage = STAGES.SCRIPT;

	// Project state
	let characters = [];
	let chapters = [];
	let panels = [];
	let pages = [];
	let palette = [];
	let creditUsage = { remaining_credits: 1000 };

	// UI & Job states
	let isJobActive = false;
	let currentJobStatus = '';
	let activeJobType = '';
	let jobError = '';
	let currentAbortController = null;

	const stageList = [
		{ id: STAGES.SCRIPT, label: '1. Script' },
		{ id: STAGES.CHARACTERS, label: '2. Characters' },
		{ id: STAGES.CHAPTERS, label: '3. Outline' },
		{ id: STAGES.PANELS, label: '4. Pages & Panels' },
		{ id: STAGES.DRAFT, label: '5. Draft Sketches' },
		{ id: STAGES.LINE_ART, label: '6. Line Art' },
		{ id: STAGES.COLOUR, label: '7. Colouring' },
		{ id: STAGES.EXPORT, label: '8. Export' }
	];

	$: scriptWordCount = project.script ? project.script.trim().split(/\s+/).filter(Boolean).length : 0;
	$: allCharactersApproved = characters.length > 0 && characters.every((c) => !!c.approved_at);
	$: allChaptersApproved = chapters.length > 0 && chapters.every((c) => !!c.approved_at);
	$: allDraftsApproved = pages.length > 0 && pages.every((p) => !!p.draft_approved_at);
	$: allLineArtApproved = pages.length > 0 && pages.every((p) => !!p.line_art_approved_at);
	$: allColoursApproved = pages.length > 0 && pages.every((p) => !!p.colour_approved_at);

	// Load initial stage data from API client
	export async function reloadProjectData() {
		if (!apiClient || !project.id) return;
		try {
			const [chars, chaps, credits] = await Promise.all([
				apiClient.getCharacters(project.id),
				apiClient.getChapters(project.id),
				apiClient.getCreditUsage()
			]);
			characters = chars;
			chapters = chaps;
			creditUsage = credits;

			if (chaps.length > 0) {
				const [pnlList, pgList] = await Promise.all([
					apiClient.getPanels(chaps[0].id),
					apiClient.getPages(chaps[0].id)
				]);
				panels = pnlList;
				pages = pgList;

				if (pgList.length > 0) {
					palette = await apiClient.getColourPalette(pgList[0].id);
				}
			}
		} catch (e) {
			console.error('Failed to load project data:', e);
		}
	}

	reloadProjectData();

	// Stage 1: Script Analysis
	async function runScriptAnalysis() {
		if (!project.script.trim()) {
			jobError = 'Script cannot be empty.';
			return;
		}
		jobError = '';
		isJobActive = true;
		activeJobType = 'Script Analysis';
		currentJobStatus = 'Analyzing characters and plot structure...';

		try {
			const res = await apiClient.analyzeScript(project.id, project.script);
			if (res.job) {
				await pollJob(apiClient, res.job.id, {
					intervalMs: 500,
					onProgress: (j) => (currentJobStatus = `Status: ${j.status}`)
				});
			}
			await reloadProjectData();
			activeStage = STAGES.CHARACTERS;
		} catch (err) {
			jobError = err.message || 'Script analysis failed';
		} finally {
			isJobActive = false;
		}
	}

	// Stage 2: Characters
	async function regenerateCharacterReference(charId) {
		jobError = '';
		isJobActive = true;
		activeJobType = 'Character Reference Generation';
		currentJobStatus = 'Rendering character reference image (Cost: $0.01)...';

		try {
			const res = await apiClient.generateCharacterReference(charId);
			if (res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			await reloadProjectData();
		} catch (err) {
			jobError = err.message || 'Character reference generation failed';
		} finally {
			isJobActive = false;
		}
	}

	async function approveCharacter(charId) {
		try {
			await apiClient.approveCharacter(charId);
			await reloadProjectData();
		} catch (err) {
			jobError = err.message;
		}
	}

	// Stage 3: Chapters
	async function approveChapter(chapId) {
		try {
			await apiClient.approveChapter(chapId);
			await reloadProjectData();
		} catch (err) {
			jobError = err.message;
		}
	}

	// Stage 5: Draft
	async function generateDraft(pageId) {
		jobError = '';
		isJobActive = true;
		activeJobType = 'Draft Sketch Generation';
		currentJobStatus = 'Generating rough composition stick draft per page (Cost: $0.01)...';

		try {
			const res = await apiClient.generateDraft(pageId);
			if (res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			await reloadProjectData();
		} catch (err) {
			jobError = err.message || 'Draft generation failed';
		} finally {
			isJobActive = false;
		}
	}

	async function approveDraft(pageId) {
		try {
			await apiClient.approveDraft(pageId);
			await reloadProjectData();
		} catch (err) {
			jobError = err.message;
		}
	}

	// Stage 6: Line Art
	async function generateLineArt(pageId) {
		jobError = '';
		isJobActive = true;
		activeJobType = 'Final Line Art Generation';
		currentJobStatus = 'Generating clean, closed-contour black & white line art (Cost: $0.01)...';

		try {
			const res = await apiClient.generateLineArt(pageId);
			if (res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			await reloadProjectData();
		} catch (err) {
			jobError = err.message || 'Line art generation failed';
		} finally {
			isJobActive = false;
		}
	}

	async function approveLineArt(pageId) {
		try {
			await apiClient.approveLineArt(pageId);
			await reloadProjectData();
		} catch (err) {
			jobError = err.message;
		}
	}

	// Stage 7: Colour
	async function handleApproveColour(pageId) {
		try {
			await apiClient.approveColour(pageId);
			await reloadProjectData();
			activeStage = STAGES.EXPORT;
		} catch (err) {
			jobError = err.message;
		}
	}

	async function suggestNewPalette(pageId) {
		jobError = '';
		isJobActive = true;
		activeJobType = 'AI Palette Suggestion';
		currentJobStatus = 'Requesting Meta vision model colour palette recommendation...';

		try {
			const res = await apiClient.suggestColourPalette(pageId);
			if (res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			if (res.palette) {
				palette = res.palette;
			}
		} catch (err) {
			jobError = err.message || 'Palette suggestion failed';
		} finally {
			isJobActive = false;
		}
	}

	function cancelActiveJob() {
		if (currentAbortController) {
			currentAbortController.abort();
		}
		isJobActive = false;
	}
</script>

<div class="comic-editor-container">
	<!-- Stage Stepper Header -->
	<nav class="stepper-nav" aria-label="Pipeline Stages">
		{#each stageList as stage}
			<button
				class="step-item"
				class:active={activeStage === stage.id}
				on:click={() => (activeStage = stage.id)}
			>
				<span class="step-title">{stage.label}</span>
			</button>
		{/each}
	</nav>

	<!-- Job Polling Progress Overlay -->
	{#if isJobActive}
		<div class="job-banner">
			<div class="spinner"></div>
			<div class="job-info">
				<strong>{activeJobType} in Progress</strong>
				<span>{currentJobStatus}</span>
			</div>
			<button class="cancel-job-btn" on:click={cancelActiveJob}>Cancel</button>
		</div>
	{/if}

	{#if jobError}
		<div class="error-banner">
			⚠️ {jobError}
		</div>
	{/if}

	<!-- Stage 1: Script Input -->
	{#if activeStage === STAGES.SCRIPT}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 1: Script Input & Chapter Selection</h3>
				<span class="meta-info">Word count: <strong>{scriptWordCount}</strong> words</span>
			</header>

			<div class="form-grid">
				<div class="form-group">
					<label for="comic-title">Comic Title</label>
					<input id="comic-title" type="text" bind:value={project.title} />
				</div>

				<div class="form-group">
					<label for="art-style">Art Style</label>
					<select id="art-style" bind:value={project.art_style}>
						<option value="cyberpunk-noir">Cyberpunk Noir</option>
						<option value="classic-superhero">Classic Superhero</option>
						<option value="manga-ink">Manga Shonen Ink</option>
						<option value="indie-graphic-novel">Indie Graphic Novel</option>
					</select>
				</div>

				<div class="form-group">
					<label for="chapter-count">Number of Chapters</label>
					<input id="chapter-count" type="number" min="1" max="10" bind:value={project.chapter_count} />
				</div>
			</div>

			<div class="form-group full-width">
				<label for="script-textarea">Comic Script (Plain text, Markdown, or Dialogue)</label>
				<textarea
					id="script-textarea"
					rows="8"
					placeholder="Enter your script here... Include scene descriptions, characters, and dialogue."
					bind:value={project.script}
				></textarea>
			</div>

			<div class="section-actions">
				<button class="primary-btn" on:click={runScriptAnalysis} disabled={isJobActive || scriptWordCount === 0}>
					⚡ Analyze Script & Extract Characters
				</button>
			</div>
		</section>

	<!-- Stage 2: Character Design -->
	{:else if activeStage === STAGES.CHARACTERS}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 2: Character Design & Reference Images</h3>
				<span class="approval-tag" class:approved={allCharactersApproved}>
					{allCharactersApproved ? '✓ All Characters Approved' : 'Approval Gate Required'}
				</span>
			</header>

			<p class="section-desc">
				Review extracted character profiles. Approved reference images anchor character stability across all comic pages.
			</p>

			<div class="cards-grid">
				{#each characters as char}
					<div class="character-card">
						<div class="char-avatar-box">
							{#if char.reference_image_url}
								<img src={char.reference_image_url} alt={char.name} class="char-img" />
							{:else}
								<div class="placeholder-avatar">No Image</div>
							{/if}
						</div>

						<div class="char-details">
							<h4>{char.name} <span class="age-badge">{char.age_range}</span></h4>
							<p><strong>Description:</strong> {char.description}</p>
							<p><strong>Appearance:</strong> {char.appearance}</p>
							<p><strong>Clothing:</strong> {char.clothing}</p>
							<p><strong>Personality:</strong> {char.personality}</p>
						</div>

						<div class="card-footer">
							<button class="secondary-btn" on:click={() => regenerateCharacterReference(char.id)}>
								🔄 Regenerate Reference ($0.01)
							</button>
							{#if char.approved_at}
								<span class="status-approved">✓ Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveCharacter(char.id)}>
									Approve Character
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					on:click={() => (activeStage = STAGES.CHAPTERS)}
					disabled={!allCharactersApproved}
				>
					Proceed to Chapter Outline →
				</button>
			</div>
		</section>

	<!-- Stage 3: Chapter Outline -->
	{:else if activeStage === STAGES.CHAPTERS}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 3: Story Outline</h3>
				<span class="approval-tag" class:approved={allChaptersApproved}>
					{allChaptersApproved ? '✓ Outline Approved' : 'Approval Gate Required'}
				</span>
			</header>

			<div class="chapters-list">
				{#each chapters as chap}
					<div class="chapter-card">
						<div class="chap-header">
							<h4>Chapter {chap.chapter_number}: {chap.title}</h4>
							{#if chap.approved_at}
								<span class="status-approved">✓ Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveChapter(chap.id)}>
									Approve Chapter
								</button>
							{/if}
						</div>
						<p class="chap-summary">{chap.summary}</p>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					on:click={() => (activeStage = STAGES.PANELS)}
					disabled={!allChaptersApproved}
				>
					Proceed to Panels & Pagination →
				</button>
			</div>
		</section>

	<!-- Stage 4: Panels & Pages -->
	{:else if activeStage === STAGES.PANELS}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 4: Panels & Page Partitioning</h3>
			</header>
			<p class="section-desc">Review and edit dialogue, narration, and page layout allocations.</p>

			<div class="panels-list">
				{#each panels as panel}
					<div class="panel-card">
						<div class="panel-num">Panel {panel.panel_number}</div>
						<div class="panel-content">
							<p><strong>Scene:</strong> {panel.scene_description}</p>
							<p><strong>Dialogue:</strong> "{panel.dialogue}"</p>
							<p><strong>Narration:</strong> <em>{panel.narration}</em></p>
							<p class="prompt-text"><strong>Prompt:</strong> {panel.image_prompt}</p>
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button class="primary-btn" on:click={() => (activeStage = STAGES.DRAFT)}>
					Proceed to Draft Sketches →
				</button>
			</div>
		</section>

	<!-- Stage 5: Draft Sketches -->
	{:else if activeStage === STAGES.DRAFT}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 5: Rough Draft Sketches (Page Composition)</h3>
				<span class="approval-tag" class:approved={allDraftsApproved}>
					{allDraftsApproved ? '✓ All Drafts Approved' : 'Approval Gate Required'}
				</span>
			</header>
			<p class="section-desc">
				One rough stick draft per page lets you approve composition at low cost ($0.01 per attempt) before committing to final line art.
			</p>

			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card">
						<h4>Page {page.page_number}</h4>
						<div class="img-container">
							{#if page.draft_image_url}
								<img src={page.draft_image_url} alt="Draft Page {page.page_number}" />
							{:else}
								<div class="empty-img">Draft Not Generated</div>
							{/if}
						</div>
						<div class="card-footer">
							<button class="secondary-btn" on:click={() => generateDraft(page.id)}>
								🔄 Regenerate Draft ($0.01)
							</button>
							{#if page.draft_approved_at}
								<span class="status-approved">✓ Draft Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveDraft(page.id)}>
									Approve Draft
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					on:click={() => (activeStage = STAGES.LINE_ART)}
					disabled={!allDraftsApproved}
				>
					Proceed to Final Line Art →
				</button>
			</div>
		</section>

	<!-- Stage 6: Final Line Art -->
	{:else if activeStage === STAGES.LINE_ART}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 6: Final Line Art</h3>
				<span class="approval-tag" class:approved={allLineArtApproved}>
					{allLineArtApproved ? '✓ Line Art Approved' : 'Approval Gate Required'}
				</span>
			</header>
			<p class="section-desc">
				High-contrast, bold comic outlines forming the fixed layer for client-side colouring.
			</p>

			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card">
						<h4>Page {page.page_number} Line Art</h4>
						<div class="img-container">
							{#if page.line_art_url}
								<img src={page.line_art_url} alt="Line Art Page {page.page_number}" />
							{:else}
								<div class="empty-img">Line Art Not Generated</div>
							{/if}
						</div>
						<div class="card-footer">
							<button class="secondary-btn" on:click={() => generateLineArt(page.id)}>
								🔄 Regenerate Line Art ($0.01)
							</button>
							{#if page.line_art_approved_at}
								<span class="status-approved">✓ Line Art Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveLineArt(page.id)}>
									Approve Line Art
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					on:click={() => (activeStage = STAGES.COLOUR)}
					disabled={!allLineArtApproved}
				>
					Proceed to Colouring Studio →
				</button>
			</div>
		</section>

	<!-- Stage 7: Colouring -->
	{:else if activeStage === STAGES.COLOUR}
		<section class="stage-section">
			<header class="section-header">
				<h3>Stage 7: Colouring Studio</h3>
				<button class="secondary-btn" on:click={() => suggestNewPalette(pages[0]?.id)}>
					🎨 Suggest AI Palette
				</button>
			</header>
			<p class="section-desc">
				Deterministic client-side bucket flood fill with undo and morphological gap-closing.
			</p>

			<ColourEditor
				lineArtUrl={pages[0]?.line_art_url || ''}
				{palette}
				onApprove={() => handleApproveColour(pages[0]?.id)}
				onPaletteChange={(newPal) => (palette = newPal)}
			/>
		</section>

	<!-- Stage 8: Export -->
	{:else if activeStage === STAGES.EXPORT}
		<section class="stage-section">
			<ExportView
				projectId={project.id}
				{apiClient}
				{pages}
			/>
		</section>
	{/if}
</div>

<style>
	.comic-editor-container {
		display: flex;
		flex-direction: column;
		gap: 1.25rem;
		font-family: system-ui, -apple-system, sans-serif;
	}
	.stepper-nav {
		display: flex;
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 0.35rem;
		gap: 0.25rem;
		overflow-x: auto;
	}
	.step-item {
		flex: 1;
		min-width: 110px;
		background: transparent;
		border: none;
		padding: 0.6rem 0.5rem;
		border-radius: 6px;
		font-size: 0.8125rem;
		font-weight: 600;
		color: #64748b;
		cursor: pointer;
		transition: all 0.15s ease;
		text-align: center;
		white-space: nowrap;
	}
	.step-item:hover {
		background: #f1f5f9;
		color: #0f172a;
	}
	.step-item.active {
		background: #0284c7;
		color: #ffffff;
	}
	.job-banner {
		display: flex;
		align-items: center;
		gap: 1rem;
		background: #eff6ff;
		border: 1px solid #bfdbfe;
		padding: 0.75rem 1rem;
		border-radius: 8px;
	}
	.spinner {
		width: 18px;
		height: 18px;
		border: 2px solid #0284c7;
		border-radius: 50%;
		border-top-color: transparent;
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.job-info {
		flex: 1;
		display: flex;
		flex-direction: column;
		font-size: 0.875rem;
		color: #1e3a8a;
	}
	.cancel-job-btn {
		background: #fee2e2;
		color: #991b1b;
		border: 1px solid #fca5a5;
		padding: 0.3rem 0.6rem;
		border-radius: 4px;
		font-size: 0.75rem;
		cursor: pointer;
	}
	.error-banner {
		background: #fef2f2;
		color: #991b1b;
		border: 1px solid #fecaca;
		padding: 0.75rem;
		border-radius: 8px;
		font-size: 0.875rem;
	}
	.stage-section {
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1.5rem;
		display: flex;
		flex-direction: column;
		gap: 1.25rem;
	}
	.section-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		border-bottom: 1px solid #f1f5f9;
		padding-bottom: 0.75rem;
	}
	.section-header h3 {
		margin: 0;
		font-size: 1.2rem;
		color: #0f172a;
	}
	.section-desc {
		margin: 0;
		color: #64748b;
		font-size: 0.875rem;
	}
	.meta-info {
		font-size: 0.875rem;
		color: #64748b;
	}
	.approval-tag {
		font-size: 0.75rem;
		font-weight: 600;
		padding: 0.25rem 0.5rem;
		border-radius: 4px;
		background: #fef3c7;
		color: #92400e;
	}
	.approval-tag.approved {
		background: #dcfce7;
		color: #166534;
	}
	.form-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
		gap: 1rem;
	}
	.form-group {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}
	.form-group.full-width {
		grid-column: 1 / -1;
	}
	.form-group label {
		font-size: 0.875rem;
		font-weight: 600;
		color: #1e293b;
	}
	.form-group input,
	.form-group select,
	.form-group textarea {
		padding: 0.5rem;
		border: 1px solid #cbd5e1;
		border-radius: 6px;
		font-size: 0.875rem;
		font-family: inherit;
	}
	.cards-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
		gap: 1rem;
	}
	.character-card {
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1rem;
		background: #f8fafc;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.char-avatar-box {
		height: 140px;
		background: #e2e8f0;
		border-radius: 6px;
		display: flex;
		align-items: center;
		justify-content: center;
		overflow: hidden;
	}
	.char-img {
		width: 100%;
		height: 100%;
		object-fit: cover;
	}
	.placeholder-avatar {
		color: #94a3b8;
		font-size: 0.875rem;
	}
	.char-details h4 {
		margin: 0 0 0.5rem 0;
		font-size: 1rem;
		color: #0f172a;
	}
	.age-badge {
		font-size: 0.75rem;
		background: #e2e8f0;
		padding: 0.15rem 0.4rem;
		border-radius: 4px;
		font-weight: normal;
		margin-left: 0.35rem;
	}
	.char-details p {
		margin: 0.25rem 0;
		font-size: 0.8125rem;
		color: #334155;
	}
	.card-footer {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-top: auto;
		padding-top: 0.5rem;
		border-top: 1px solid #e2e8f0;
	}
	.status-approved {
		color: #16a34a;
		font-weight: 600;
		font-size: 0.8125rem;
	}
	.primary-btn {
		background: #0284c7;
		color: white;
		border: none;
		padding: 0.6rem 1.25rem;
		border-radius: 6px;
		font-weight: 600;
		font-size: 0.875rem;
		cursor: pointer;
	}
	.primary-btn:hover:not(:disabled) {
		background: #0369a1;
	}
	.primary-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.secondary-btn {
		background: #ffffff;
		border: 1px solid #cbd5e1;
		color: #334155;
		padding: 0.35rem 0.75rem;
		border-radius: 6px;
		font-size: 0.8125rem;
		font-weight: 500;
		cursor: pointer;
	}
	.approve-btn {
		background: #16a34a;
		color: white;
		border: none;
		padding: 0.35rem 0.75rem;
		border-radius: 6px;
		font-size: 0.8125rem;
		font-weight: 600;
		cursor: pointer;
	}
	.chapters-list,
	.panels-list {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.chapter-card,
	.panel-card {
		border: 1px solid #e2e8f0;
		border-radius: 6px;
		padding: 1rem;
		background: #f8fafc;
	}
	.chap-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.chap-header h4 {
		margin: 0;
	}
	.chap-summary {
		margin: 0.5rem 0 0 0;
		font-size: 0.875rem;
		color: #334155;
	}
	.panel-num {
		font-weight: 600;
		font-size: 0.875rem;
		color: #0284c7;
		margin-bottom: 0.35rem;
	}
	.panel-content p {
		margin: 0.25rem 0;
		font-size: 0.8125rem;
	}
	.prompt-text {
		color: #64748b;
		font-family: monospace;
	}
	.page-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
		gap: 1rem;
	}
	.page-preview-card {
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1rem;
		background: #f8fafc;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.page-preview-card h4 {
		margin: 0;
		font-size: 0.95rem;
	}
	.img-container {
		height: 240px;
		background: #ffffff;
		border: 1px solid #cbd5e1;
		border-radius: 4px;
		display: flex;
		align-items: center;
		justify-content: center;
		overflow: hidden;
	}
	.img-container img {
		max-width: 100%;
		max-height: 100%;
		object-fit: contain;
	}
	.empty-img {
		color: #94a3b8;
		font-size: 0.875rem;
	}
	.section-actions {
		display: flex;
		justify-content: flex-end;
		padding-top: 0.75rem;
		border-top: 1px solid #f1f5f9;
	}
</style>
