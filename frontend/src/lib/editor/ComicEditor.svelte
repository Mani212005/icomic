<script>
	import ColourEditor from '$lib/colour/ColourEditor.svelte';
	import ExportView from '$lib/export/ExportView.svelte';
	import { pollJob } from '$lib/api/jobPolling.js';
	import { STAGES, isApprovalStale, hasValidApproval, formatApiError } from '$lib/api/types.js';

	export let project = {
		id: 'proj-demo-1',
		title: 'Demo',
		art_style: 'cyberpunk-noir',
		chapter_count: 3,
		script: '',
		original_script: ''
	};
	export let apiClient;
	export let activeStage = STAGES.SCRIPT;

	let characters = [];
	let chapters = [];
	let panels = [];
	let pages = [];
	let palette = [];
	let creditUsage = { remaining_credits: 1000 };

	let isJobActive = false;
	let currentJobStatus = '';
	let activeJobType = '';
	let jobError = null;
	let currentAbortController = null;

	let editingCharId = null;
	let editCharForm = { name: '', description: '', appearance: '', clothing: '' };

	let editingChapId = null;
	let editChapTitle = '';
	let editChapSummary = '';

	let editingPanelId = null;
	let editPanelDialogue = '';
	let editPanelScene = '';

	const stageList = [
		{ id: STAGES.SCRIPT, label: '1. Script' },
		{ id: STAGES.CHARACTERS, label: '2. Characters' },
		{ id: STAGES.CHAPTERS, label: '3. Outline' },
		{ id: STAGES.PANELS, label: '4. Panels' },
		{ id: STAGES.DRAFT, label: '5. Drafts' },
		{ id: STAGES.LINE_ART, label: '6. Lines' },
		{ id: STAGES.COLOUR, label: '7. Colour' },
		{ id: STAGES.EXPORT, label: '8. Export' }
	];

	$: effectiveScript = project ? (project.original_script || project.script || '') : '';
	$: scriptWordCount = effectiveScript ? effectiveScript.trim().split(/\s+/).filter(Boolean).length : 0;

	$: anyCharacterStale = characters.some((c) => isApprovalStale(c.approved_at, c.updated_at));
	$: allCharactersApproved = characters.length > 0 && characters.every((c) => hasValidApproval(c.approved_at, c.updated_at));

	$: anyChapterStale = chapters.some((c) => isApprovalStale(c.approved_at, c.updated_at));
	$: allChaptersApproved =
		chapters.length > 0 &&
		chapters.every((c) => hasValidApproval(c.approved_at || (c.status === 'approved' ? c.updated_at : null), c.updated_at));

	$: anyDraftStale = pages.some((p) => isApprovalStale(p.draft_approved_at, p.updated_at));
	$: allDraftsApproved = pages.length > 0 && pages.every((p) => hasValidApproval(p.draft_approved_at, p.updated_at));

	$: anyLineArtStale = pages.some((p) => isApprovalStale(p.line_art_approved_at, p.updated_at));
	$: allLineArtApproved = pages.length > 0 && pages.every((p) => hasValidApproval(p.line_art_approved_at, p.updated_at));

	$: allColoursApproved = pages.length > 0 && pages.every((p) => hasValidApproval(p.colour_approved_at, p.updated_at));

	let lastProjectId = null;
	$: if (project && project.id && project.id !== lastProjectId) {
		lastProjectId = project.id;
		reloadProjectData();
	}

	export async function reloadProjectData() {
		if (!apiClient || !project || !project.id) return;
		try {
			const [chars, chaps, credits] = await Promise.all([
				apiClient.getCharacters(project.id),
				apiClient.getChapters(project.id),
				apiClient.getCreditUsage()
			]);
			characters = Array.isArray(chars) ? chars : (chars?.characters || []);
			chapters = Array.isArray(chaps) ? chaps : (chaps?.chapters || []);
			creditUsage = credits || { remaining_credits: 0 };

			if (chapters.length > 0) {
				const [pnlList, pgList] = await Promise.all([
					apiClient.getPanels(chapters[0].id),
					apiClient.getPages(chapters[0].id)
				]);
				panels = Array.isArray(pnlList) ? pnlList : (pnlList?.panels || []);
				pages = Array.isArray(pgList) ? pgList : (pgList?.pages || []);

				if (pages.length > 0) {
					const pal = await apiClient.getColourPalette(pages[0].id);
					palette = Array.isArray(pal) ? pal : (pal?.palette || []);
				}
			} else {
				panels = [];
				pages = [];
			}
		} catch (e) {
			console.error('Project:', e);
			jobError = formatApiError(e);
		}
	}

	reloadProjectData();

	async function runScriptAnalysis() {
		const scriptText = (project.original_script || project.script || '').trim();
		if (!scriptText) {
			jobError = formatApiError('Add script.');
			return;
		}
		jobError = null;
		isJobActive = true;
		activeJobType = 'Script';
		currentJobStatus = 'Analyzing…';

		try {

			await apiClient.updateProject(project.id, {
				title: project.title,
				art_style: project.art_style,
				chapter_count: Number(project.chapter_count) || 1,
				original_script: scriptText,
				script: scriptText
			});

			const res = await apiClient.analyzeScript(project.id, scriptText);
			if (res && res.job) {
				await pollJob(apiClient, res.job.id, {
					intervalMs: 500,
					onProgress: (j) => (currentJobStatus = `Status: ${j.status}`)
				});
			}
			await reloadProjectData();
			activeStage = STAGES.CHARACTERS;
		} catch (err) {
			jobError = formatApiError(err);
		} finally {
			isJobActive = false;
		}
	}

	async function regenerateCharacterReference(charId) {
		jobError = null;
		isJobActive = true;
		activeJobType = 'Reference';
		currentJobStatus = 'Rendering…';

		try {
			const res = await apiClient.generateCharacterReference(charId);
			if (res && res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		} finally {
			isJobActive = false;
		}
	}

	async function approveCharacter(charId) {
		jobError = null;
		try {
			await apiClient.approveCharacter(charId);
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	function startEditChar(char) {
		editingCharId = char.id;
		editCharForm = {
			name: char.name || '',
			description: char.description || '',
			appearance: (char.visual_attributes && char.visual_attributes.appearance) || char.appearance || '',
			clothing: (char.visual_attributes && char.visual_attributes.clothing) || char.clothing || ''
		};
	}

	function cancelEditChar() {
		editingCharId = null;
	}

	async function saveEditChar(charId) {
		jobError = null;
		try {
			await apiClient.updateCharacter(charId, {
				name: editCharForm.name,
				description: editCharForm.description,
				appearance: editCharForm.appearance,
				clothing: editCharForm.clothing,
				visual_attributes: {
					appearance: editCharForm.appearance,
					clothing: editCharForm.clothing
				}
			});
			editingCharId = null;
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	async function approveChapter(chapId) {
		jobError = null;
		try {
			await apiClient.approveChapter(chapId);
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	function startEditChap(chap) {
		editingChapId = chap.id;
		editChapTitle = chap.title || '';
		editChapSummary = chap.summary || '';
	}

	function cancelEditChap() {
		editingChapId = null;
	}

	async function saveEditChap(chapId) {
		jobError = null;
		try {
			await apiClient.updateChapter(chapId, {
				title: editChapTitle,
				summary: editChapSummary
			});
			editingChapId = null;
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	function startEditPanel(panel) {
		editingPanelId = panel.id;
		editPanelDialogue = panel.dialogue || '';
		editPanelScene = panel.scene_description || '';
	}

	function cancelEditPanel() {
		editingPanelId = null;
	}

	async function saveEditPanel(panelId) {
		jobError = null;
		try {
			await apiClient.updatePanel(panelId, {
				dialogue: editPanelDialogue,
				scene_description: editPanelScene
			});
			editingPanelId = null;
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	async function generateDraft(pageId) {
		jobError = null;
		isJobActive = true;
		activeJobType = 'Draft';
		currentJobStatus = 'Generating…';

		try {
			const res = await apiClient.generateDraft(pageId);
			if (res && res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		} finally {
			isJobActive = false;
		}
	}

	async function approveDraft(pageId) {
		jobError = null;
		try {
			await apiClient.approveDraft(pageId);
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	async function generateLineArt(pageId) {
		jobError = null;
		isJobActive = true;
		activeJobType = 'Line art';
		currentJobStatus = 'Generating…';

		try {
			const res = await apiClient.generateLineArt(pageId);
			if (res && res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		} finally {
			isJobActive = false;
		}
	}

	async function approveLineArt(pageId) {
		jobError = null;
		try {
			await apiClient.approveLineArt(pageId);
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	async function handleApproveColour(pageId) {
		jobError = null;
		try {
			await apiClient.approveColour(pageId);
			await reloadProjectData();
			activeStage = STAGES.EXPORT;
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	async function suggestNewPalette(pageId) {
		jobError = null;
		isJobActive = true;
		activeJobType = 'Palette';
		currentJobStatus = 'Suggesting…';

		try {
			const res = await apiClient.suggestColourPalette(pageId);
			if (res && res.job) {
				await pollJob(apiClient, res.job.id, { intervalMs: 500 });
			}
			if (res && res.palette) {
				palette = res.palette;
			}
		} catch (err) {
			jobError = formatApiError(err);
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

	<nav class="stepper-nav" aria-label="Stages">
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

	{#if isJobActive}
		<div class="job-banner">
			<div class="spinner"></div>
			<div class="job-info">
				<strong>{activeJobType}…</strong>
				<span>{currentJobStatus}</span>
			</div>
			<button class="cancel-job-btn" on:click={cancelActiveJob}>Cancel</button>
		</div>
	{/if}

	{#if jobError}
		<div class="error-banner error-{jobError.type}">
			<div class="error-header">
				<span class="error-icon">⚠️</span>
				<strong class="error-title">{jobError.title}</strong>
				<button class="dismiss-btn" on:click={() => (jobError = null)} aria-label="Dismiss error">✕</button>
			</div>
			<p class="error-msg">{jobError.message}</p>
			{#if jobError.action}
				<div class="error-action">
					<span class="action-tag">Next:</span> {jobError.action}
				</div>
			{/if}
		</div>
	{/if}

	{#if activeStage === STAGES.SCRIPT}
		<section class="stage-section">
			<header class="section-header">
				<h3>Script</h3>
				<span class="meta-info">Words: <strong>{scriptWordCount}</strong></span>
			</header>

			<div class="form-grid">
				<div class="form-group">
					<label for="comic-title">Title</label>
					<input id="comic-title" type="text" bind:value={project.title} />
				</div>

				<div class="form-group">
					<label for="art-style">Style</label>
					<select id="art-style" bind:value={project.art_style}>
						<option value="cyberpunk-noir">Cyberpunk</option>
						<option value="classic-superhero">Superhero</option>
						<option value="manga-ink">Shonen ink</option>
						<option value="indie-graphic-novel">Indie novel</option>
					</select>
				</div>

				<div class="form-group">
					<label for="chapter-count">Chapters</label>
					<input id="chapter-count" type="number" min="1" max="10" bind:value={project.chapter_count} />
				</div>
			</div>

			<div class="form-group full-width">
				<label for="script-textarea">Script</label>
				<textarea
					id="script-textarea"
					rows="8"
					placeholder="Write scenes and dialogue…"
					bind:value={project.script}
				></textarea>
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					aria-label="⚡ Analyze script"
					on:click={runScriptAnalysis}
					disabled={isJobActive || scriptWordCount === 0}
				>
					⚡ Analyze
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.CHARACTERS}
		<section class="stage-section">
			<header class="section-header">
				<h3>Characters</h3>
				<div class="header-tags">
					{#if anyCharacterStale}
						<span class="approval-tag stale">⚠️ Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allCharactersApproved}>
							{allCharactersApproved ? '✓ Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyCharacterStale}
				<div class="stale-stage-banner">
					<strong>⚠️ Edited:</strong> Re-approve.
				</div>
			{/if}

			<div class="cards-grid">
				{#each characters as char}
					<div class="character-card" class:card-stale={isApprovalStale(char.approved_at, char.updated_at)}>
						<div class="char-avatar-box">
							{#if char.reference_image_url}
								<img src={char.reference_image_url} alt={char.name} class="char-img" />
							{:else}
								<div class="placeholder-avatar">No image</div>
							{/if}
						</div>

						{#if editingCharId === char.id}
							<div class="char-edit-form">
								<label>Name: <input type="text" bind:value={editCharForm.name} /></label>
								<label>Bio: <textarea rows="2" bind:value={editCharForm.description}></textarea></label>
								<label>Look: <input type="text" bind:value={editCharForm.appearance} /></label>
								<label>Clothes: <input type="text" bind:value={editCharForm.clothing} /></label>
								<div class="edit-actions">
									<button class="primary-btn small" on:click={() => saveEditChar(char.id)}>Save</button>
									<button class="secondary-btn small" on:click={cancelEditChar}>Cancel</button>
								</div>
							</div>
						{:else}
							<div class="char-details">
								<div class="char-title-row">
									<h4>{char.name} <span class="age-badge">{char.visual_attributes?.age_range || char.age_range || ''}</span></h4>
									<button class="edit-icon-btn" on:click={() => startEditChar(char)}>✏️ Edit</button>
								</div>
								<p><strong>Bio:</strong> {char.description}</p>
								<p><strong>Look:</strong> {char.visual_attributes?.appearance || char.appearance || 'Standard'}</p>
								<p><strong>Clothes:</strong> {char.visual_attributes?.clothing || char.clothing || 'Standard'}</p>
							</div>
						{/if}

						<div class="card-footer">
							<button class="secondary-btn" on:click={() => regenerateCharacterReference(char.id)} aria-label="🔄 Regenerate ref ($0.01)">
								🔄 Regen ($0.01)
							</button>

							{#if isApprovalStale(char.approved_at, char.updated_at)}
								<div class="stale-action-box">
									<span class="stale-warning-text">⚠️ Edited</span>
									<button class="reapprove-btn" on:click={() => approveCharacter(char.id)}>
										Re-approve
									</button>
								</div>
							{:else if char.approved_at}
								<span class="status-approved">✓ Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveCharacter(char.id)}>
									Approve
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					aria-label="Next: outline"
					on:click={() => (activeStage = STAGES.CHAPTERS)}
					disabled={!allCharactersApproved}
				>
					Continue →
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.CHAPTERS}
		<section class="stage-section">
			<header class="section-header">
				<h3>Outline</h3>
				<div class="header-tags">
					{#if anyChapterStale}
						<span class="approval-tag stale">⚠️ Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allChaptersApproved}>
							{allChaptersApproved ? '✓ Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyChapterStale}
				<div class="stale-stage-banner">
					<strong>⚠️ Edited.</strong> Re-approve.
				</div>
			{/if}

			<div class="chapters-list">
				{#each chapters as chap}
					<div class="chapter-card" class:card-stale={isApprovalStale(chap.approved_at, chap.updated_at)}>
						<div class="chap-header">
							{#if editingChapId === chap.id}
								<div class="chap-edit-row">
									<input type="text" bind:value={editChapTitle} />
									<button class="primary-btn small" on:click={() => saveEditChap(chap.id)}>Save</button>
									<button class="secondary-btn small" on:click={cancelEditChap}>Cancel</button>
								</div>
							{:else}
								<h4>Chapter {chap.chapter_number}: {chap.title}</h4>
								<button class="edit-icon-btn" on:click={() => startEditChap(chap)}>✏️ Edit</button>
							{/if}

							<div class="chap-approval-box">
								{#if isApprovalStale(chap.approved_at, chap.updated_at)}
									<span class="stale-warning-text">⚠️ Edited</span>
									<button class="reapprove-btn" on:click={() => approveChapter(chap.id)}>
										Re-approve
									</button>
								{:else if chap.approved_at || chap.status === 'approved'}
									<span class="status-approved">✓ Approved</span>
								{:else}
									<button class="approve-btn" on:click={() => approveChapter(chap.id)}>
										Approve
									</button>
								{/if}
							</div>
						</div>

						{#if editingChapId === chap.id}
							<textarea rows="3" bind:value={editChapSummary}></textarea>
						{:else}
							<p class="chap-summary">{chap.summary}</p>
						{/if}
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					aria-label="Next: panels"
					on:click={() => (activeStage = STAGES.PANELS)}
					disabled={!allChaptersApproved}
				>
					Continue →
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.PANELS}
		<section class="stage-section">
			<header class="section-header">
				<h3>Panels</h3>
			</header>

			<div class="panels-list">
				{#each panels as panel}
					<div class="panel-card">
						<div class="panel-header-row">
							<span class="panel-num">Panel {panel.panel_number}</span>
							{#if editingPanelId === panel.id}
								<div class="panel-edit-btns">
									<button class="primary-btn small" on:click={() => saveEditPanel(panel.id)}>Save</button>
									<button class="secondary-btn small" on:click={cancelEditPanel}>Cancel</button>
								</div>
							{:else}
								<button class="edit-icon-btn" on:click={() => startEditPanel(panel)} aria-label="✏️ Edit Panel">✏️ Edit</button>
							{/if}
						</div>

						{#if editingPanelId === panel.id}
							<div class="panel-edit-form">
								<label>Scene: <textarea rows="2" bind:value={editPanelScene}></textarea></label>
								<label>Dialogue: <input type="text" bind:value={editPanelDialogue} /></label>
							</div>
						{:else}
							<div class="panel-content">
								<p><strong>Scene:</strong> {panel.scene_description}</p>
								<p><strong>Dialogue:</strong> "{panel.dialogue}"</p>
								<p><strong>Narration:</strong> <em>{panel.narration}</em></p>
								<p class="prompt-text"><strong>Prompt:</strong> {panel.image_prompt}</p>
							</div>
						{/if}
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button class="primary-btn" on:click={() => (activeStage = STAGES.DRAFT)} aria-label="Next: drafts">
					Continue →
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.DRAFT}
		<section class="stage-section">
			<header class="section-header">
				<h3>Drafts</h3>
				<div class="header-tags">
					{#if anyDraftStale}
						<span class="approval-tag stale">⚠️ Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allDraftsApproved}>
							{allDraftsApproved ? '✓ Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyDraftStale}
				<div class="stale-stage-banner">
					<strong>⚠️ Edited:</strong> Re-approve.
				</div>
			{/if}

			<p class="section-desc">Approve each draft.</p>

			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card" class:card-stale={isApprovalStale(page.draft_approved_at, page.updated_at)}>
						<h4>Page {page.page_number}</h4>
						<div class="img-container">
							{#if page.draft_image_url}
								<img src={page.draft_image_url} alt="Draft {page.page_number}" />
							{:else}
								<div class="empty-img">No draft</div>
							{/if}
						</div>
						<div class="card-footer">
			<button class="secondary-btn" on:click={() => generateDraft(page.id)} aria-label="🔄 Regen draft ($0.01)">
								🔄 Regen ($0.01)
							</button>

							{#if isApprovalStale(page.draft_approved_at, page.updated_at)}
								<div class="stale-action-box">
									<span class="stale-warning-text">⚠️ Edited</span>
									<button class="reapprove-btn" on:click={() => approveDraft(page.id)}>
										Re-approve
									</button>
								</div>
							{:else if page.draft_approved_at}
								<span class="status-approved">✓ Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveDraft(page.id)}>
									Approve
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					aria-label="Next: line art"
					on:click={() => (activeStage = STAGES.LINE_ART)}
					disabled={!allDraftsApproved}
				>
					Continue →
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.LINE_ART}
		<section class="stage-section">
			<header class="section-header">
				<h3>Line art</h3>
				<div class="header-tags">
					{#if anyLineArtStale}
						<span class="approval-tag stale">⚠️ Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allLineArtApproved}>
							{allLineArtApproved ? '✓ Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyLineArtStale}
				<div class="stale-stage-banner">
					<strong>⚠️ Edited:</strong> Re-approve.
				</div>
			{/if}

			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card" class:card-stale={isApprovalStale(page.line_art_approved_at, page.updated_at)}>
						<h4>Page {page.page_number}</h4>
						<div class="img-container">
							{#if page.line_art_image_url || page.line_art_url}
								<img src={page.line_art_image_url || page.line_art_url} alt="Page {page.page_number}" />
							{:else}
								<div class="empty-img">No line art</div>
							{/if}
						</div>
						<div class="card-footer">
			<button class="secondary-btn" on:click={() => generateLineArt(page.id)} aria-label="🔄 Regen line art ($0.01)">
								🔄 Regen ($0.01)
							</button>

							{#if isApprovalStale(page.line_art_approved_at, page.updated_at)}
								<div class="stale-action-box">
									<span class="stale-warning-text">⚠️ Edited</span>
									<button class="reapprove-btn" on:click={() => approveLineArt(page.id)}>
										Re-approve
									</button>
								</div>
							{:else if page.line_art_approved_at}
								<span class="status-approved">✓ Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveLineArt(page.id)}>
									Approve
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					aria-label="Next: colour"
					on:click={() => (activeStage = STAGES.COLOUR)}
					disabled={!allLineArtApproved}
				>
					Continue →
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.COLOUR}
		<section class="stage-section">
			<header class="section-header">
				<h3>Colour</h3>
				<button class="secondary-btn" on:click={() => suggestNewPalette(pages[0]?.id)}>
					🎨 Suggest palette
				</button>
			</header>

			<ColourEditor
				lineArtUrl={pages[0]?.line_art_image_url || pages[0]?.line_art_url || ''}
				{palette}
				onApprove={() => handleApproveColour(pages[0]?.id)}
				onPaletteChange={(newPal) => (palette = newPal)}
			/>
		</section>

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
		border-left: 4px solid #dc2626;
		padding: 0.875rem 1rem;
		border-radius: 8px;
		font-size: 0.875rem;
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}
	.error-banner.error-stale_approval {
		background: #fffbeb;
		color: #92400e;
		border-color: #fde68a;
		border-left-color: #d97706;
	}
	.error-banner.error-kill_switch {
		background: #fdf2f8;
		color: #9d174d;
		border-color: #fbcfe8;
		border-left-color: #db2777;
	}
	.error-banner.error-quota_exceeded {
		background: #fef3c7;
		color: #78350f;
		border-color: #fde68a;
		border-left-color: #b45309;
	}
	.error-banner.error-moderation_rejected {
		background: #fef2f2;
		color: #b91c1c;
		border-color: #fca5a5;
		border-left-color: #ef4444;
	}
	.error-banner.error-unauthorized {
		background: #f8fafc;
		color: #334155;
		border-color: #cbd5e1;
		border-left-color: #475569;
	}
	.error-header {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.error-title {
		font-weight: 700;
		font-size: 0.9375rem;
		flex: 1;
	}
	.dismiss-btn {
		background: transparent;
		border: none;
		color: inherit;
		cursor: pointer;
		font-size: 0.875rem;
		padding: 0.2rem 0.4rem;
		border-radius: 4px;
	}
	.dismiss-btn:hover {
		background: rgba(0, 0, 0, 0.05);
	}
	.error-msg {
		margin: 0;
		line-height: 1.4;
	}
	.error-action {
		font-size: 0.8125rem;
		background: rgba(255, 255, 255, 0.6);
		padding: 0.3rem 0.5rem;
		border-radius: 4px;
	}
	.action-tag {
		font-weight: 600;
	}

	.stale-stage-banner {
		background: #fffbeb;
		border: 1px solid #fde68a;
		border-radius: 6px;
		padding: 0.6rem 0.875rem;
		font-size: 0.8125rem;
		color: #92400e;
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
	.header-tags {
		display: flex;
		gap: 0.5rem;
		align-items: center;
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
	.approval-tag.stale {
		background: #fee2e2;
		color: #991b1b;
		border: 1px solid #fca5a5;
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
		transition: border-color 0.15s ease;
	}
	.character-card.card-stale,
	.page-preview-card.card-stale,
	.chapter-card.card-stale {
		border-color: #f59e0b;
		background: #fffdf5;
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
	.char-title-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
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
	.char-edit-form {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
		font-size: 0.8125rem;
	}
	.char-edit-form input,
	.char-edit-form textarea {
		width: 100%;
		box-sizing: border-box;
		font-family: inherit;
		font-size: 0.8125rem;
		padding: 0.3rem 0.4rem;
		border: 1px solid #cbd5e1;
		border-radius: 4px;
	}
	.edit-actions {
		display: flex;
		gap: 0.5rem;
		margin-top: 0.3rem;
	}
	.edit-icon-btn {
		background: transparent;
		border: 1px solid #e2e8f0;
		border-radius: 4px;
		font-size: 0.75rem;
		cursor: pointer;
		padding: 0.2rem 0.4rem;
		color: #64748b;
	}
	.edit-icon-btn:hover {
		background: #f1f5f9;
		color: #0f172a;
	}
	.card-footer {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-top: auto;
		padding-top: 0.5rem;
		border-top: 1px solid #e2e8f0;
	}
	.stale-action-box {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 0.25rem;
	}
	.stale-warning-text {
		color: #d97706;
		font-size: 0.7rem;
		font-weight: 600;
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
	.primary-btn.small {
		padding: 0.3rem 0.6rem;
		font-size: 0.75rem;
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
	.secondary-btn.small {
		padding: 0.25rem 0.5rem;
		font-size: 0.75rem;
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
	.approve-btn:hover {
		background: #15803d;
	}
	.reapprove-btn {
		background: #d97706;
		color: white;
		border: none;
		padding: 0.35rem 0.75rem;
		border-radius: 6px;
		font-size: 0.8125rem;
		font-weight: 600;
		cursor: pointer;
	}
	.reapprove-btn:hover {
		background: #b45309;
	}

	.chapters-list,
	.panels-list {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.chapter-card,
	.panel-card {
		background: #f8fafc;
		border: 1px solid #e2e8f0;
		border-radius: 6px;
		padding: 0.875rem;
	}
	.chap-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 0.5rem;
	}
	.chap-header h4 {
		margin: 0;
		color: #0f172a;
		font-size: 0.9375rem;
	}
	.chap-edit-row {
		display: flex;
		gap: 0.5rem;
		align-items: center;
		flex: 1;
	}
	.chap-edit-row input {
		flex: 1;
		padding: 0.3rem 0.5rem;
		border: 1px solid #cbd5e1;
		border-radius: 4px;
	}
	.chapter-card textarea {
		width: 100%;
		box-sizing: border-box;
		padding: 0.4rem;
		border: 1px solid #cbd5e1;
		border-radius: 4px;
		font-family: inherit;
		font-size: 0.8125rem;
	}
	.chap-summary {
		margin: 0;
		font-size: 0.8125rem;
		color: #475569;
		line-height: 1.4;
	}
	.chap-approval-box {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	@media (max-width: 600px) {
		.chap-header {
			flex-wrap: wrap;
		}
		.chap-header h4 {
			flex: 1 1 100%;
		}
		.chap-header .edit-icon-btn {
			margin-left: auto;
		}
		.chap-approval-box {
			margin-left: 0.5rem;
		}
	}

	.panel-header-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 0.5rem;
	}
	.panel-num {
		font-weight: 700;
		font-size: 0.8125rem;
		color: #0284c7;
	}
	.panel-content p {
		margin: 0.2rem 0;
		font-size: 0.8125rem;
		color: #334155;
	}
	.panel-edit-form {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
		font-size: 0.8125rem;
	}
	.panel-edit-form textarea,
	.panel-edit-form input {
		width: 100%;
		box-sizing: border-box;
		padding: 0.3rem;
		border: 1px solid #cbd5e1;
		border-radius: 4px;
		font-family: inherit;
		font-size: 0.8125rem;
	}
	.prompt-text {
		color: #64748b !important;
		font-size: 0.75rem !important;
	}

	.page-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
		gap: 1.25rem;
	}
	.page-preview-card {
		background: #f8fafc;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.page-preview-card h4 {
		margin: 0;
		font-size: 0.9375rem;
		color: #0f172a;
	}
	.img-container {
		height: 320px;
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 6px;
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
</style>
