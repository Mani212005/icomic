<script>
	import ErrorBanner from './ErrorBanner.svelte';
	import Icon from '#lib/design/Icon.svelte';
	import ColourEditor from '#lib/colour/ColourEditor.svelte';
	import ExportView from '#lib/export/ExportView.svelte';
	import { pollJob } from '#lib/api/jobPolling.js';
	import { STAGES, isApprovalStale, hasValidApproval, formatApiError, formatCostQuote } from '#lib/api/types.js';
	const INK_VARIANT = 'ink';

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
	let stageNavOpen = false;
	let stageDrawerToggle;
	let exportCompleted = false;
	function selectStage(id) {
		activeStage = id;
		stageNavOpen = false;
		if (stageDrawerToggle?.offsetParent) stageDrawerToggle.focus();
	}
	function handleStageNavKeydown(event) {
		if (event.key === 'Escape' && stageNavOpen) {
			stageNavOpen = false;
			stageDrawerToggle.focus();
		}
	}
	function markExported() {
		exportCompleted = true;
	}

	let characters = [];
	let chapters = [];
	let panels = [];
	let pages = [];
	let palette = [];
	let finishVariant = 'painted';
	let palettesByPage = {};
	let failedImages = new Set();
	function markImageFailed(event) {
		failedImages = new Set([...failedImages, event.currentTarget.getAttribute('src')]);
	}
	let creditUsage = { remaining_credits: 1000 };
	let costQuotes = {};
	function quoteLabel(stage) {
		return formatCostQuote(costQuotes[stage]);
	}

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

	function normalizePalette(rawPalette) {
		if (Array.isArray(rawPalette)) return rawPalette;
		return Object.entries(rawPalette || {}).map(([region_name, hex_color]) => ({
			region_name,
			hex_color: typeof hex_color === 'string' ? hex_color : hex_color?.hex_color,
			label: typeof hex_color === 'string' ? region_name : (hex_color?.label || region_name)
		})).filter((item) => typeof item.hex_color === 'string');
	}

	function updatePagePalette(pageId, newPalette) {
		palettesByPage = { ...palettesByPage, [pageId]: newPalette };
		if (pages[0]?.id === pageId) palette = newPalette;
	}

	const stageList = [
		{ id: STAGES.SCRIPT, label: '1. Script' },
		{ id: STAGES.CHARACTERS, label: '2. Characters' },
		{ id: STAGES.CHAPTERS, label: '3. Outline' },
		{ id: STAGES.PANELS, label: '4. Panels' },
		{ id: STAGES.DRAFT, label: '5. Drafts' },
		{ id: STAGES.LINE_ART, label: '6. Finish' },
		{ id: STAGES.COLOUR, label: '7. Ink' },
		{ id: STAGES.EXPORT, label: '8. Export' }
	];

	$: currentStage = stageList.find((stage) => stage.id === activeStage);
	$: stageCompleted = {
		[STAGES.SCRIPT]: characters.length > 0,
		[STAGES.CHARACTERS]: allCharactersApproved,
		[STAGES.CHAPTERS]: allChaptersApproved,
		[STAGES.PANELS]: panels.length > 0,
		[STAGES.DRAFT]: allDraftsApproved,
		[STAGES.LINE_ART]: allLineArtApproved,
		[STAGES.COLOUR]: allColoursApproved,
		[STAGES.EXPORT]: exportCompleted
	};

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

	$: anyLineArtStale = pages.some((p) => isApprovalStale(finishApprovalAt(p), p.updated_at));
	$: allLineArtApproved = pages.length > 0 && pages.every((p) => hasValidApproval(finishApprovalAt(p), p.updated_at));
	function finishApprovalAt(page) {
		if (finishVariant === INK_VARIANT) return page.line_art_approved_at;
		return page.painted_page_image_url ? page.painted_page_approved_at : page.line_art_approved_at;
	}
	function finishImageUrl(page) {
		if (finishVariant === INK_VARIANT) return page.line_art_image_url || page.line_art_url;
		return page.painted_page_image_url || page.line_art_image_url || page.line_art_url;
	}

	$: allColoursApproved = pages.length > 0 && pages.every((p) => hasValidApproval(p.colour_approved_at, p.updated_at));

	let lastProjectId = null;
	$: if (project && project.id && project.id !== lastProjectId) {
		lastProjectId = project.id;
		exportCompleted = false;
		stageNavOpen = false;
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
			failedImages = new Set();
			characters = Array.isArray(chars) ? chars : (chars?.characters || []);
			chapters = Array.isArray(chaps) ? chaps : (chaps?.chapters || []);
			creditUsage = credits || { remaining_credits: 0 };

			if (chapters.length > 0) {
				const chapterData = await Promise.all(chapters.map(async (chapter) => {
					const [pnlList, pgList] = await Promise.all([
						apiClient.getPanels(chapter.id), apiClient.getPages(chapter.id)
					]);
					return {
						panels: (Array.isArray(pnlList) ? pnlList : (pnlList?.panels || [])).map((panel) => ({ ...panel, chapter_number: chapter.chapter_number })),
						pages: (Array.isArray(pgList) ? pgList : (pgList?.pages || [])).map((page) => ({ ...page, chapter_number: chapter.chapter_number }))
					};
				}));
				panels = chapterData.flatMap((chapter) => chapter.panels);
				pages = chapterData.flatMap((chapter) => chapter.pages);

				const loadedPalettes = await Promise.all(pages.map((page) => apiClient.getColourPalette(page.id)));
				palettesByPage = Object.fromEntries(pages.map((page, index) => [page.id, normalizePalette(loadedPalettes[index]?.palette || loadedPalettes[index])]));
				palette = pages.length > 0 ? palettesByPage[pages[0].id] : [];
			} else {
				panels = [];
				pages = [];
				palettesByPage = {};
				palette = [];
			}
			const stages = ['character_reference', 'draft_sketch', 'final_line_art', 'colour_suggestion'];
			const quotes = await Promise.all(stages.map(async (stage) => {
				try {
					return [stage, await apiClient.estimateCost(project.id, stage, 1)];
				} catch {
					return [stage, null];
				}
			}));
			costQuotes = Object.fromEntries(quotes);
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

	async function generateChapters() {
		jobError = null;
		isJobActive = true;
		activeJobType = stageList.find((stage) => stage.id === activeStage).label.slice(3);
		currentJobStatus = 'Generating…';
		try {
			await apiClient.createChapters(project.id, { chapter_count: project.chapter_count });
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		} finally {
			isJobActive = false;
		}
	}

	async function generatePanels() {
		jobError = null;
		isJobActive = true;
		activeJobType = stageList.find((stage) => stage.id === activeStage).label.slice(3);
		currentJobStatus = 'Generating…';
		try {
			for (const chapter of chapters) {
				const existing = await apiClient.getPanels(chapter.id);
				if ((Array.isArray(existing) ? existing : (existing?.panels || [])).length === 0) {
					await apiClient.createPanels(chapter.id, {});
				}
			}
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		} finally {
			isJobActive = false;
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
		activeJobType = finishVariant === INK_VARIANT ? 'Ink' : 'Paint';
		currentJobStatus = 'Generating…';

		try {
			const res = await apiClient.generateLineArt(pageId, finishVariant);
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
			await apiClient.approveLineArt(pageId, finishVariant);
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	async function handleApproveColour(pageId, colouredImage) {
		jobError = null;
		try {
			if (colouredImage && typeof apiClient.uploadColourImage === 'function') {
				await apiClient.uploadColourImage(pageId, colouredImage);
			}
			await apiClient.approveColour(pageId);
			await reloadProjectData();
			if (pages.length > 0 && pages.every((page) => hasValidApproval(page.colour_approved_at, page.updated_at))) {
				activeStage = STAGES.EXPORT;
			}
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
				updatePagePalette(pageId, normalizePalette(res.palette));
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

	<aside class="stage-sidebar">
		<button class="stage-drawer-toggle" on:keydown={handleStageNavKeydown} bind:this={stageDrawerToggle} aria-expanded={stageNavOpen} aria-controls="stage-navigation" on:click={() => stageNavOpen = !stageNavOpen}>
			<Icon name={currentStage.id} size={22} />
			<span>{currentStage.label}</span>
			<span class="drawer-chevron" class:open={stageNavOpen}><Icon name="arrow" size={18} /></span>
		</button>
	<nav id="stage-navigation" class="stepper-nav" class:open={stageNavOpen} aria-label="Stages">
		{#each stageList as stage}
			<button
				class="step-item"
				on:keydown={handleStageNavKeydown}
				class:active={activeStage === stage.id}
				class:complete={stageCompleted[stage.id]}
				aria-current={activeStage === stage.id ? 'step' : undefined}
				on:click={() => selectStage(stage.id)}
			>
				<Icon name={stage.id} size={22} />
				<span class="step-title">{stage.label}</span>
				{#if stageCompleted[stage.id]}<span class="completion-mark"><Icon name="check" label="Complete" size={15} /></span>{/if}
			</button>
		{/each}
	</nav>
	</aside>
	<div class="stage-workspace">

	{#if isJobActive}
		<div class="job-banner" role="status">
			<div class="spinner"></div>
			<div class="job-info">
				<strong>{activeJobType}…</strong>
				<span>{currentJobStatus}</span>
			</div>
			<button class="cancel-job-btn" on:click={cancelActiveJob}>Cancel</button>
		</div>
	{/if}

	<ErrorBanner error={jobError} onDismiss={() => (jobError = null)} />

	{#if activeStage === STAGES.SCRIPT}
		<section class="stage-section">
			<header class="section-header">
				<h2>Script</h2>
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
					<input id="chapter-count" type="number" min="1" bind:value={project.chapter_count} />
				</div>
			</div>

			<div class="form-group full-width">
				<label for="script-textarea">Script</label>
				<textarea
					id="script-textarea"
					rows="8"
					bind:value={project.script}
				></textarea>
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					
					on:click={runScriptAnalysis}
					disabled={isJobActive || scriptWordCount === 0}
				>
					<Icon name="script" size={18} /> Analyze
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.CHARACTERS}
		<section class="stage-section">
			<header class="section-header">
				<h2>Characters</h2>
				<div class="header-tags">
					{#if anyCharacterStale}
						<span class="approval-tag stale"><Icon name="warning" size={18} /> Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allCharactersApproved}>
							{#if allCharactersApproved}<Icon name="check" size={14} />{/if}{allCharactersApproved ? 'Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyCharacterStale}
				<div class="stale-stage-banner">
					<Icon name="warning" size={18} /> Re-approve.
				</div>
			{/if}

			{#if characters.length === 0}<div class="stage-empty"><Icon name="characters" size={48} /></div>{/if}
			<div class="cards-grid">
				{#each characters as char}
					<div class="character-card" class:card-stale={isApprovalStale(char.approved_at, char.updated_at)}>
						<div class="char-avatar-box">
							{#if char.reference_image_url && !failedImages.has(char.reference_image_url)}
								<img src={char.reference_image_url} alt={char.name} class="char-img" on:error={markImageFailed} />
							{:else}
								<div class="placeholder-avatar"><Icon name="characters" size={48} />Pending</div>
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
									<h3>{char.name} <span class="age-badge">{char.visual_attributes?.age_range || char.age_range || ''}</span></h3>
									{#if char.is_sample || char.sample_label}
										<span class="sample-tag">Sample</span>
									{/if}
									<button class="edit-icon-btn" on:click={() => startEditChar(char)}><Icon name="edit" size={18} /> Edit</button>
								</div>
								<p><strong>Bio:</strong> {char.description}</p>
								{#if char.visual_attributes?.reference_role === 'extra'}
									<p>Reference image optional.</p>
								{/if}
								<p><strong>Look:</strong> {char.visual_attributes?.appearance || char.appearance || 'Standard'}</p>
								<p><strong>Clothes:</strong> {char.visual_attributes?.clothing || char.clothing || 'Standard'}</p>
							</div>
						{/if}

						<div class="card-footer">
							<button class="secondary-btn" on:click={() => regenerateCharacterReference(char.id)}>
								<Icon name="regenerate" size={18} /> Regen ({quoteLabel('character_reference')})
							</button>

							{#if isApprovalStale(char.approved_at, char.updated_at)}
								<div class="stale-action-box">
									<span class="stale-warning-text"><Icon name="warning" size={18} /> Edited</span>
									<button class="reapprove-btn" on:click={() => approveCharacter(char.id)}>
										Re-approve
									</button>
								</div>
							{:else if char.approved_at}
								<span class="status-approved"><Icon name="check" size={18} /> Approved</span>
							{:else}
								<button class="approve-btn" on:click={() => approveCharacter(char.id)}>
									{char.visual_attributes?.reference_role === 'extra' && !char.reference_image_url ? 'Approve profile' : 'Approve'}
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
					Continue <Icon name="arrow" size={18} />
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.CHAPTERS}
		<section class="stage-section">
			<header class="section-header">
				<h2>Outline</h2>
				<div class="header-tags">
					{#if anyChapterStale}
						<span class="approval-tag stale"><Icon name="warning" size={18} /> Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allChaptersApproved}>
							{#if allChaptersApproved}<Icon name="check" size={14} />{/if}{allChaptersApproved ? 'Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyChapterStale}
				<div class="stale-stage-banner">
					<Icon name="warning" size={18} /> Re-approve.
				</div>
			{/if}

			{#if chapters.length === 0}
				<div class="stage-empty"><Icon name="chapters" size={48} /></div>
				<button class="primary-btn" on:click={generateChapters} disabled={isJobActive || !allCharactersApproved}>Generate outline</button>
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
								<h3>Chapter {chap.chapter_number}: {chap.title}</h3>
								<button class="edit-icon-btn" on:click={() => startEditChap(chap)}><Icon name="edit" size={18} /> Edit</button>
							{/if}

							<div class="chap-approval-box">
								{#if isApprovalStale(chap.approved_at, chap.updated_at)}
									<span class="stale-warning-text"><Icon name="warning" size={18} /> Edited</span>
									<button class="reapprove-btn" on:click={() => approveChapter(chap.id)}>
										Re-approve
									</button>
								{:else if chap.approved_at || chap.status === 'approved'}
									<span class="status-approved"><Icon name="check" size={18} /> Approved</span>
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
					on:click={() => (activeStage = STAGES.PANELS)}
					disabled={!allChaptersApproved}
				>
					Continue <Icon name="arrow" size={18} />
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.PANELS}
		<section class="stage-section">
			<header class="section-header">
				<h2>Panels</h2>
			</header>

			<p class="section-desc">{panels.length} panels across {pages.length} pages</p>
			{#if chapters.some((chapter) => !panels.some((panel) => panel.chapter_id === chapter.id))}
				<button class="primary-btn" on:click={generatePanels} disabled={isJobActive || !allChaptersApproved}>Generate panels</button>
			{/if}
			{#if panels.length === 0}<div class="stage-empty"><Icon name="panels" size={48} /></div>{/if}
			<div class="panels-list">
				{#each panels as panel}
					<div class="panel-card">
						<div class="panel-header-row">
							<span class="panel-num">Chapter {panel.chapter_number}, Panel {panel.panel_number}</span>
							{#if editingPanelId === panel.id}
								<div class="panel-edit-btns">
									<button class="primary-btn small" on:click={() => saveEditPanel(panel.id)}>Save</button>
									<button class="secondary-btn small" on:click={cancelEditPanel}>Cancel</button>
								</div>
							{:else}
								<button class="edit-icon-btn" on:click={() => startEditPanel(panel)} ><Icon name="edit" size={18} /> Edit</button>
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
				<button class="primary-btn" on:click={() => (activeStage = STAGES.DRAFT)} >
					Continue <Icon name="arrow" size={18} />
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.DRAFT}
		<section class="stage-section">
			<header class="section-header">
				<h2>Drafts</h2>
				<div class="header-tags">
					{#if anyDraftStale}
						<span class="approval-tag stale"><Icon name="warning" size={18} /> Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allDraftsApproved}>
							{#if allDraftsApproved}<Icon name="check" size={14} />{/if}{allDraftsApproved ? 'Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyDraftStale}
				<div class="stale-stage-banner">
					<Icon name="warning" size={18} /> Re-approve.
				</div>
			{/if}


			{#if pages.length === 0}<div class="stage-empty"><Icon name="draft" size={48} /></div>{/if}
			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card" class:card-stale={isApprovalStale(page.draft_approved_at, page.updated_at)}>
						<div class="card-title-row">
							<h3>Chapter {page.chapter_number}, Page {page.page_number}</h3>
							{#if page.is_sample || page.sample_label}
								<span class="sample-tag">Sample</span>
							{/if}
						</div>
						<div class="img-container">
							{#if page.draft_image_url && !failedImages.has(page.draft_image_url)}
								<img src={page.draft_image_url} alt="Draft {page.page_number}" on:error={markImageFailed} />
							{:else}
								<div class="empty-img"><Icon name="draft" size={48} />Pending</div>
							{/if}
						</div>
						<div class="card-footer">
							<button class="secondary-btn" on:click={() => generateDraft(page.id)}>
								<Icon name="regenerate" size={18} /> Regen ({quoteLabel('draft_sketch')})
							</button>

							{#if isApprovalStale(page.draft_approved_at, page.updated_at)}
								<div class="stale-action-box">
									<span class="stale-warning-text"><Icon name="warning" size={18} /> Edited</span>
									<button class="reapprove-btn" on:click={() => approveDraft(page.id)}>
										Re-approve
									</button>
								</div>
							{:else if page.draft_approved_at}
								<span class="status-approved"><Icon name="check" size={18} /> Approved</span>
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
					on:click={() => (activeStage = STAGES.LINE_ART)}
					disabled={!allDraftsApproved}
				>
					Continue <Icon name="arrow" size={18} />
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.LINE_ART}
		<section class="stage-section">
			<header class="section-header">
				<label class="finish-variant">
					<select bind:value={finishVariant} aria-label="Finish">
						<option value="painted">Painted</option>
						<option value="ink">Ink</option>
					</select>
				</label>
				<div class="header-tags">
					{#if anyLineArtStale}
						<span class="approval-tag stale"><Icon name="warning" size={18} /> Stale</span>
					{:else}
						<span class="approval-tag" class:approved={allLineArtApproved}>
							{#if allLineArtApproved}<Icon name="check" size={14} />{/if}{allLineArtApproved ? 'Approved' : 'Pending'}
						</span>
					{/if}
				</div>
			</header>

			{#if anyLineArtStale}
				<div class="stale-stage-banner">
					<Icon name="warning" size={18} /> Re-approve.
				</div>
			{/if}

			{#if pages.length === 0}<div class="stage-empty"><Icon name="draft" size={48} /></div>{/if}
			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card" class:card-stale={isApprovalStale(finishApprovalAt(page), page.updated_at)}>
						<div class="card-title-row">
							<h3>Chapter {page.chapter_number}, Page {page.page_number}</h3>
							{#if page.is_sample || page.sample_label}
								<span class="sample-tag">Sample</span>
							{/if}
						</div>
						<div class="img-container">
							{#if finishImageUrl(page) && !failedImages.has(finishImageUrl(page))}
								<img src={finishImageUrl(page)} alt="Page {page.page_number}" on:error={markImageFailed} />
							{:else}
								<div class="empty-img"><Icon name="line_art" size={48} />Pending</div>
							{/if}
						</div>
						<div class="card-footer">
							<button class="secondary-btn" on:click={() => generateLineArt(page.id)} aria-label="Regenerate">
								<Icon name="regenerate" size={18} /> Regen ({quoteLabel('final_line_art')})
							</button>

							{#if isApprovalStale(finishApprovalAt(page), page.updated_at)}
								<div class="stale-action-box">
									<span class="stale-warning-text"><Icon name="warning" size={18} /> Edited</span>
									<button class="reapprove-btn" on:click={() => approveLineArt(page.id)}>
										Re-approve
									</button>
								</div>
							{:else if hasValidApproval(finishApprovalAt(page), page.updated_at)}
								<span class="status-approved"><Icon name="check" size={18} /> Approved</span>
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
					on:click={() => (activeStage = finishVariant === INK_VARIANT ? STAGES.COLOUR : STAGES.EXPORT)}
					disabled={!allLineArtApproved}
				>
					{finishVariant === INK_VARIANT ? 'Colour' : 'Export'} <Icon name="arrow" size={18} />
				</button>
			</div>
		</section>

	{:else if activeStage === STAGES.COLOUR}
		<section class="stage-section">
			<header class="section-header">
				<h2>Colour</h2>
			</header>

		{#if pages.length === 0}<div class="stage-empty"><Icon name="colour" size={48} /></div>{/if}
		{#each pages as page (page.id)}
			<div class="page-colour-editor">
				<div class="page-colour-heading">
					<h3>Page {page.page_number}</h3>
					<button class="secondary-btn" on:click={() => suggestNewPalette(page.id)}>
						<Icon name="colour" size={18} /> Suggest palette ({quoteLabel('colour_suggestion')})
					</button>
				</div>
				<ColourEditor
					lineArtUrl={page.line_art_image_url || page.line_art_url || ''}
					palette={palettesByPage[page.id] || []}
					onApprove={(colouredImage) => handleApproveColour(page.id, colouredImage)}
					onPaletteChange={(newPalette) => updatePagePalette(page.id, newPalette)}
				/>
			</div>
		{/each}
		</section>

	{:else if activeStage === STAGES.EXPORT}
		<section class="stage-section">
			<ExportView
				projectId={project.id}
				{apiClient}
				{pages}
				onComplete={markExported}
			/>
		</section>
	{/if}
	</div>
</div>
