<script>
	import { tick } from 'svelte';
	import ErrorBanner from './ErrorBanner.svelte';
	import { PANEL_FIELDS, emptyPanelForm } from './panelFields.js';
	import { pageOfPanel, moveTarget } from './panelLayout.js';
	import { stageList, quoteStages } from './stages.js';
	import { Icon, BetaTag, revealCards } from '#lib/design/shell.js';
	import { ApprovalTag, ApprovalAction, ColourEditor, LetteringPreview, ExportView, LetteringSession, pollJob, GenerationRunner, JOB_KIND } from './features.js';
	import { STAGES, APPROVAL, approvalState, isApprovalStale, hasValidApproval, formatApiError, formatCostQuote, publishSpend, INK_OVERLAY_NOTE, isOverlayPage } from '#lib/api/types.js';
	const GENERATING = 'Generating…';
	const INK_VARIANT = 'ink';
	const PAINTED_VARIANT = 'painted';
	const NEW_PAGE = 'new';

	export let project;
	export let apiClient;
	export let activeStage = STAGES.SCRIPT;
	export let onProgress = null;
	let exportCompleted = false;
	export async function selectStage(id) {
		activeStage = id;
		if (id === STAGES.PANELS) {
			await tick();
			await Promise.all([...letteringSessions.values()].map((session) => session.pendingRequest));
			await reloadProjectData();
		}
	}
	function markExported() {
		exportCompleted = true;
	}

	let characters = [];
	let chapters = [];
	let panels = [];
	let pages = [];
	let letteringSessions = new Map();
	function letteringSession(page) {
		if (!letteringSessions.has(page.id)) letteringSessions.set(page.id, new LetteringSession(apiClient, page.id));
		return letteringSessions.get(page.id);
	}
	let palette = [];
	let finishVariant = PAINTED_VARIANT;
	let rawPreviewByPage = {};
	let palettesByPage = {};
	let failedImages = new Set();
	function markImageFailed(event) {
		failedImages = new Set([...failedImages, event.currentTarget.getAttribute('src')]);
	}
	let creditUsage = { remaining_credits: 1000 };
	let costQuotes = {};
	function quoteLabel(stage, quotes) {
		return formatCostQuote(quotes[stage]);
	}

	let isJobActive = false;
	let currentJobStatus = '';
	let activeJobType = '';
	let jobError = null;
	let jobStalled = '';
	let resumeLabel = '';
	let canCancel = false;
	let cancelling = false;
	let jobSettling = false;
	let jobNotice = '';
	const runner = new GenerationRunner({
		get api() {
			return apiClient;
		},
		settle: settleJob,
		onChange: (view) => {
			isJobActive = view.active;
			activeJobType = view.label;
			currentJobStatus = view.status;
			jobStalled = view.stalled;
			resumeLabel = view.resumeLabel;
			canCancel = view.canCancel;
			cancelling = view.cancelling;
			jobSettling = view.settling;
			jobNotice = view.notice;
		},
		onError: (err) => (jobError = formatApiError(err))
	});

	let editingCharId = null;
	let editCharForm = { name: '', description: '', appearance: '', clothing: '' };

	let editingChapId = null;
	let editChapForm = { title: '', summary: '', events: '', cast: '', ending: '' };

	let editingPanelId = null;
	let editPanelForm = emptyPanelForm();
	// What the last analysis said about the script, shown on the Characters stage it leads to.
	let analysisWarnings = [];

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

	async function changeFinishPreset(event) {
		const finish_preset = event.currentTarget.value;
		try {
			project = await apiClient.updateProject(project.id, { finish_preset });
			pages = await Promise.all(pages.map(refreshFinishedPreviews));
		} catch (error) {
			jobError = formatApiError(error);
		}
	}

	async function refreshFinishedPreviews(page) {
		const request = async (variant) => {
			try {
				return await apiClient.request(`/api/v1/pages/${page.id}/finish?variant=${variant}`, { method: 'POST' });
			} catch {
				return {};
			}
		};
		const [painted, ink] = await Promise.all([request('painted'), request('ink')]);
		return {
			...page,
			raw_master_image_url: painted.raw_master_image_url || page.painted_page_image_url || page.line_art_image_url,
			finished_image_url: painted.finished_image_url || '',
			raw_ink_image_url: ink.raw_master_image_url || page.line_art_image_url,
			finished_ink_image_url: ink.finished_image_url || ''
		};
	}

	function finishPreviewUrl(page) {
		const finished = finishVariant === INK_VARIANT ? page.finished_ink_image_url : page.finished_image_url;
		const raw = finishVariant === INK_VARIANT ? page.raw_ink_image_url : page.raw_master_image_url;
		return rawPreviewByPage[page.id]
			? (raw || finishImageUrl(page))
			: (finished || finishImageUrl(page));
	}


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
	$: onProgress?.(stageCompleted);

	$: effectiveScript = project ? (project.original_script || project.script || '') : '';
	$: scriptWordCount = effectiveScript ? effectiveScript.trim().split(/\s+/).filter(Boolean).length : 0;

	// A stage is stale when anything in it was approved and has changed since; approved when every item
	// has a current approval; otherwise pending. Items never approved are pending, not stale.
	const stageState = (items, approvedAt) => {
		if (items.some((item) => isApprovalStale(approvedAt(item), item.updated_at))) return APPROVAL.STALE;
		const done = items.length > 0 && items.every((item) => hasValidApproval(approvedAt(item), item.updated_at));
		return done ? APPROVAL.APPROVED : APPROVAL.PENDING;
	};
	$: characterStage = stageState(characters, (c) => c.approved_at);
	$: anyCharacterStale = characterStage === APPROVAL.STALE;
	$: allCharactersApproved = characterStage === APPROVAL.APPROVED;

	// An outline carries its approval in `status`: the server sets it on approval and sets it back to
	// pending when the outline text is edited, so a chapter is either approved or still pending.
	const isChapterApproved = (chapter) => chapter.status === 'approved';
	$: allChaptersApproved = chapters.length > 0 && chapters.every(isChapterApproved);

	$: draftStage = stageState(pages, (p) => p.draft_approved_at);
	$: anyDraftStale = draftStage === APPROVAL.STALE;
	$: allDraftsApproved = draftStage === APPROVAL.APPROVED;

	$: finishStage = stageState(pages, finishApprovalAt);
	$: anyLineArtStale = finishStage === APPROVAL.STALE;
	$: allLineArtApproved = finishStage === APPROVAL.APPROVED;
	// Ink is baked-lettering only, so say so up front: overlay pages offer Painted only and
	// never fail with a 409 at click time.
	$: overlayPages = pages.filter(isOverlayPage);
	$: allOverlay = pages.length > 0 && overlayPages.length === pages.length;
	$: if (allOverlay && finishVariant === INK_VARIANT) finishVariant = PAINTED_VARIANT;
	$: inkBlockedByOverlay = finishVariant === INK_VARIANT && overlayPages.length > 0;
	// The variant is a parameter so templates re-evaluate when it changes.
	function inkBlocked(page, variant) {
		return variant === INK_VARIANT && isOverlayPage(page);
	}
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
		letteringSessions = new Map();
		exportCompleted = false;
		analysisWarnings = [];
		reloadProjectData();
		runner.setProject(project.id);
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
			publishSpend(credits);

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
				pages = await Promise.all(chapterData.flatMap((chapter) => chapter.pages).map(refreshFinishedPreviews));
				await Promise.all(pages.map((page) => {
					const session = letteringSessions.get(page.id);
					return session && !session.value.busy ? session.load(true) : undefined;
				}));

				const loadedPalettes = await Promise.all(pages.map((page) => apiClient.getColourPalette(page.id)));
				palettesByPage = Object.fromEntries(pages.map((page, index) => [page.id, normalizePalette(loadedPalettes[index]?.palette || loadedPalettes[index])]));
				palette = pages.length > 0 ? palettesByPage[pages[0].id] : [];
			} else {
				panels = [];
				pages = [];
				palettesByPage = {};
				palette = [];
			}
			const quotes = await Promise.all(quoteStages.map(async (stage) => {
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

	// What a finished job changes on screen, by the kind the receipt was started for.
	async function settleJob(job) {
		if (job.kind === JOB_KIND.PALETTE) {
			const saved = await apiClient.getColourPalette(job.entityId);
			updatePagePalette(job.entityId, normalizePalette(saved?.palette || saved));
			return;
		}
		await reloadProjectData();
		if (job.kind === JOB_KIND.SCRIPT) activeStage = STAGES.CHARACTERS;
	}

	const keepWaiting = () => runner.follow();
	const cancelGeneration = () => runner.cancel();

	async function runScriptAnalysis() {
		const scriptText = (project.original_script || project.script || '').trim();
		if (!scriptText) {
			jobError = formatApiError('Add script.');
			return;
		}
		jobError = null;
		analysisWarnings = [];
		await runner.run({
			kind: JOB_KIND.SCRIPT,
			label: 'Script',
			status: 'Analyzing…',
			start: async () => {
				await apiClient.updateProject(project.id, {
					title: project.title,
					art_style: project.art_style,
					chapter_count: Number(project.chapter_count) || 1,
					original_script: scriptText,
					script: scriptText
				});
				const receipt = await apiClient.analyzeScript(project.id, scriptText);
				analysisWarnings = Array.isArray(receipt?.warnings) ? receipt.warnings : [];
				return receipt;
			}
		});
	}

	function regenerateCharacterReference(charId) {
		jobError = null;
		return runner.run({
			kind: JOB_KIND.CHARACTER,
			label: 'Reference',
			status: 'Rendering…',
			entityId: charId,
			start: (idempotencyKey) => apiClient.generateCharacterReference(charId, { idempotencyKey })
		});
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
				// The server merges these into the stored attributes, so age, hair, reference role and
				// anything else the form does not show are kept.
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
		currentJobStatus = GENERATING;
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
		currentJobStatus = GENERATING;
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

	const toLines = (items) => (Array.isArray(items) ? items : []).join('\n');
	const fromLines = (text) => text.split('\n').map((line) => line.trim()).filter(Boolean);

	function startEditChap(chap) {
		editingChapId = chap.id;
		editChapForm = {
			title: chap.title || '',
			summary: chap.summary || '',
			events: toLines(chap.important_events),
			cast: toLines(chap.character_names),
			ending: chap.ending || ''
		};
	}

	function cancelEditChap() {
		editingChapId = null;
	}

	async function saveEditChap(chapId) {
		jobError = null;
		try {
			await apiClient.updateChapter(chapId, {
				title: editChapForm.title,
				summary: editChapForm.summary,
				important_events: fromLines(editChapForm.events),
				character_names: fromLines(editChapForm.cast),
				ending: editChapForm.ending
			});
			editingChapId = null;
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	// Structure edits (add, move, delete) are one request each; the server stales every page they change.
	// They run one at a time, and the chapter is reloaded after each, even after a refusal, so the screen
	// shows what is stored if another tab changed the layout meanwhile.
	let layoutBusy = false;
	let deletingPanelId = null;
	let addingToChapterId = null;
	let addPanelForm = emptyPanelForm();
	let addPanelPageId = NEW_PAGE;
	const chapterPages = (chapterId, allPages) => allPages.filter((page) => page.chapter_id === chapterId);
	const hasPanels = (chapterId, allPanels) => allPanels.some((panel) => panel.chapter_id === chapterId);

	async function editLayout(edit) {
		jobError = null;
		layoutBusy = true;
		let done = false;
		try {
			await edit();
			done = true;
		} catch (err) {
			jobError = formatApiError(err);
		}
		await reloadProjectData();
		layoutBusy = false;
		return done;
	}

	function stepPanel(panel, direction) {
		const target = moveTarget(chapterPages(panel.chapter_id, pages), panel.id, direction);
		if (target) editLayout(() => apiClient.movePanel(panel.chapter_id, panel.id, target.pageId, target.position));
	}

	async function deletePanel(panel) {
		if (await editLayout(() => apiClient.deletePanel(panel.chapter_id, panel.id))) deletingPanelId = null;
	}

	function startAddPanel(chapter) {
		const last = chapterPages(chapter.id, pages).at(-1);
		addingToChapterId = chapter.id;
		addPanelForm = emptyPanelForm();
		addPanelPageId = last ? last.id : NEW_PAGE;
	}

	async function addPanel(chapterId) {
		const fields = Object.fromEntries(PANEL_FIELDS.map(([key]) => [key, addPanelForm[key].trim()]).filter(([, value]) => value));
		const payload = addPanelPageId === NEW_PAGE ? fields : { ...fields, page_id: addPanelPageId };
		if (await editLayout(() => apiClient.addPanel(chapterId, payload))) addingToChapterId = null;
	}

	function startEditPanel(panel) {
		editingPanelId = panel.id;
		editPanelForm = Object.fromEntries(PANEL_FIELDS.map(([key]) => [key, panel[key] || '']));
	}

	function cancelEditPanel() {
		editingPanelId = null;
	}

	async function saveEditPanel(panelId) {
		jobError = null;
		try {
			// Only fields that changed are sent: any save marks the pages holding the panel as edited,
			// so a save with nothing changed must not undo their approvals.
			const stored = panels.find((panel) => panel.id === panelId) || {};
			const changes = Object.fromEntries(
				PANEL_FIELDS.filter(([key]) => editPanelForm[key] !== (stored[key] || '')).map(([key]) => [key, editPanelForm[key]])
			);
			if (Object.keys(changes).length > 0) await apiClient.updatePanel(panelId, changes);
			editingPanelId = null;
			await reloadProjectData();
		} catch (err) {
			jobError = formatApiError(err);
		}
	}

	function generateDraft(pageId) {
		jobError = null;
		return runner.run({
			kind: JOB_KIND.PAGE,
			label: 'Draft',
			status: GENERATING,
			entityId: pageId,
			start: (idempotencyKey) => apiClient.generateDraft(pageId, { idempotencyKey })
		});
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

	function generateLineArt(pageId) {
		const variant = finishVariant;
		jobError = null;
		return runner.run({
			kind: JOB_KIND.PAGE,
			label: variant === INK_VARIANT ? 'Ink' : 'Paint',
			status: GENERATING,
			entityId: pageId,
			start: (idempotencyKey) => apiClient.generateLineArt(pageId, variant, { idempotencyKey })
		});
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

	function suggestNewPalette(pageId) {
		jobError = null;
		return runner.run({
			kind: JOB_KIND.PALETTE,
			label: 'Palette',
			status: 'Suggesting…',
			entityId: pageId,
			start: (idempotencyKey) => apiClient.suggestColourPalette(pageId, { idempotencyKey })
		});
	}
</script>

{#snippet staleBanner()}
	<div class="stale-stage-banner"><Icon name="warning" size={18} /> Changed since approval. Approve again.</div>
{/snippet}

<div class="comic-editor-container" use:revealCards>

	<div class="stage-workspace">

	{#if isJobActive}
		<div class="job-banner" class:settling={jobSettling} role="status" data-state={jobSettling ? 'settling' : 'active'}>
			<div class="spinner"></div>
			<div class="job-info">
				<strong>{activeJobType}…</strong>
				<span>{jobStalled || currentJobStatus}</span>
			</div>
			{#if jobStalled}
				<button class="secondary-btn small" on:click={keepWaiting}>{resumeLabel}</button>
			{/if}
			{#if canCancel || cancelling}
				<button class="secondary-btn small" on:click={cancelGeneration} disabled={cancelling}>
					{cancelling ? 'Cancelling…' : 'Cancel generation'}
				</button>
			{/if}
		</div>
	{:else if jobNotice}
		<div class="job-banner job-notice" role="status" data-state="notice">
			<div class="job-info"><span>{jobNotice}</span></div>
			<button class="secondary-btn small" on:click={() => runner.clearNotice()}>Dismiss</button>
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
					bind:value={project.original_script}
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
					<ApprovalTag state={characterStage} />
				</div>
			</header>

			{#if anyCharacterStale}
				{@render staleBanner()}
			{/if}
			{#if analysisWarnings.length > 0}
				<ul class="analysis-warnings" aria-label="Notes">
					{#each analysisWarnings as warning}
						<li><Icon name="warning" size={18} /> {warning}</li>
					{/each}
				</ul>
			{/if}

			{#if characters.length === 0}<div class="stage-empty"><Icon name="characters" size={48} /></div>{/if}
			<div class="cards-grid">
				{#each characters as char}
					<div class="character-card" class:card-stale={isApprovalStale(char.approved_at, char.updated_at)}>
						<div class="char-avatar-box">
							{#if char.reference_image_url && !failedImages.has(char.reference_image_url)}
								<img src={char.reference_image_url} alt={char.name} class="char-img" on:error={markImageFailed} />
							{:else}
								<div class="placeholder-avatar"><Icon name="characters" size={48} /></div>
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
										<BetaTag />
									{/if}
									<button class="edit-icon-btn" on:click={() => startEditChar(char)}><Icon name="edit" size={18} /> Edit</button>
								</div>
								<p><strong>Bio:</strong> {char.description}</p>
								{#if char.visual_attributes?.reference_role === 'extra'}
								<p>Optional.</p>
								{/if}
								<p><strong>Look:</strong> {char.visual_attributes?.appearance || char.appearance || 'Standard'}</p>
								<p><strong>Clothes:</strong> {char.visual_attributes?.clothing || char.clothing || 'Standard'}</p>
							</div>
						{/if}

						<div class="card-footer">
							<button class="secondary-btn" disabled={isJobActive} on:click={() => regenerateCharacterReference(char.id)}>
								<Icon name="regenerate" size={18} /> Regen ({quoteLabel('character_reference', costQuotes)})
							</button>

							<ApprovalAction
								state={approvalState(char.approved_at, char.updated_at)}
								label={char.visual_attributes?.reference_role === 'extra' && !char.reference_image_url ? 'Approve profile' : 'Approve'}
								onApprove={() => approveCharacter(char.id)}
							/>
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
					<ApprovalTag state={allChaptersApproved ? APPROVAL.APPROVED : APPROVAL.PENDING} />
				</div>
			</header>

			{#if chapters.length === 0}
				<div class="stage-empty"><Icon name="chapters" size={48} /></div>
				<button class="primary-btn" on:click={generateChapters} disabled={isJobActive || !allCharactersApproved}>Outline</button>
			{/if}
			<div class="chapters-list">
				{#each chapters as chap}
					<div class="chapter-card">
						<div class="chap-header">
							{#if editingChapId === chap.id}
								<div class="chap-edit-row">
									<input type="text" bind:value={editChapForm.title} />
									<button class="primary-btn small" on:click={() => saveEditChap(chap.id)}>Save</button>
									<button class="secondary-btn small" on:click={cancelEditChap}>Cancel</button>
								</div>
							{:else}
								<h3>Chapter {chap.chapter_number}: {chap.title}</h3>
								<button class="edit-icon-btn" on:click={() => startEditChap(chap)}><Icon name="edit" size={18} /> Edit</button>
							{/if}

							<div class="chap-approval-box">
								<ApprovalAction
									state={isChapterApproved(chap) ? APPROVAL.APPROVED : APPROVAL.PENDING}
									onApprove={() => approveChapter(chap.id)}
								/>
							</div>
						</div>

						{#if editingChapId === chap.id}
							<div class="chap-edit-form">
								<label>Summary: <textarea rows="3" bind:value={editChapForm.summary}></textarea></label>
								<label>Ending: <input type="text" bind:value={editChapForm.ending} /></label>
								<label>Events: <textarea rows="4" bind:value={editChapForm.events}></textarea></label>
								<label>Cast: <textarea rows="2" bind:value={editChapForm.cast}></textarea></label>
								<small>One per line</small>
							</div>
						{:else}
							<p class="chap-summary">{chap.summary}</p>
							<div class="chap-detail">
								{#if chap.important_events?.length}
									<p><strong>Events:</strong></p>
									<ul>
										{#each chap.important_events as event}<li>{event}</li>{/each}
									</ul>
								{/if}
								{#if chap.character_names?.length}
									<p><strong>Cast:</strong> {chap.character_names.join(', ')}</p>
								{/if}
								{#if chap.ending}
									<p><strong>Ending:</strong> {chap.ending}</p>
								{/if}
							</div>
						{/if}
					</div>
				{/each}
			</div>

			<div class="section-actions">
				<button
					class="primary-btn"
					on:click={() => selectStage(STAGES.PANELS)}
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

			<p class="section-desc">{panels.length} panels, {pages.length} pages</p>
			{#if chapters.some((chapter) => !panels.some((panel) => panel.chapter_id === chapter.id))}
				<button class="primary-btn" on:click={generatePanels} disabled={isJobActive || !allChaptersApproved}>Panels</button>
			{/if}
			{#if panels.length === 0}<div class="stage-empty"><Icon name="panels" size={48} /></div>{/if}
			{#each chapters.filter((chapter) => hasPanels(chapter.id, panels)) as chapter (chapter.id)}
				{@const chapterPanels = panels.filter((panel) => panel.chapter_id === chapter.id)}
				{@const pagesOfChapter = chapterPages(chapter.id, pages)}
				<div class="panels-list">
					{#each chapterPanels as panel (panel.id)}
						{@const page = pageOfPanel(pagesOfChapter, panel.id)}
						<div class="panel-card">
							<div class="panel-header-row">
								<span class="panel-num">Chapter {panel.chapter_number}, Panel {panel.panel_number}{#if page} <span class="page-chip">Page {page.page_number}</span>{/if}</span>
								{#if editingPanelId === panel.id}
									<div class="panel-edit-btns">
										<button class="primary-btn small" on:click={() => saveEditPanel(panel.id)}>Save</button>
										<button class="secondary-btn small" on:click={cancelEditPanel}>Cancel</button>
									</div>
								{:else}
									<div class="panel-tools">
										<button class="edit-icon-btn" aria-label="Move panel {panel.panel_number} up" title="Move up" disabled={layoutBusy || !moveTarget(pagesOfChapter, panel.id, 'up')} on:click={() => stepPanel(panel, 'up')}><span class="turn-up"><Icon name="arrow" size={18} /></span></button>
										<button class="edit-icon-btn" aria-label="Move panel {panel.panel_number} down" title="Move down" disabled={layoutBusy || !moveTarget(pagesOfChapter, panel.id, 'down')} on:click={() => stepPanel(panel, 'down')}><span class="turn-down"><Icon name="arrow" size={18} /></span></button>
										<button class="edit-icon-btn" on:click={() => startEditPanel(panel)} ><Icon name="edit" size={18} /> Edit</button>
										<button class="edit-icon-btn" aria-label="Delete panel {panel.panel_number}" disabled={layoutBusy} on:click={() => (deletingPanelId = panel.id)}><Icon name="delete" size={18} /> Delete</button>
									</div>
								{/if}
							</div>

							{#if deletingPanelId === panel.id}
								<div class="panel-confirm" role="alertdialog" aria-label="Delete panel {panel.panel_number}">
									<p><Icon name="warning" size={18} /> Delete panel {panel.panel_number}? The pages that hold it need their drafts and approvals again.</p>
									<div class="edit-actions">
										<button class="primary-btn small" disabled={layoutBusy} on:click={() => deletePanel(panel)}>Delete panel</button>
										<button class="secondary-btn small" disabled={layoutBusy} on:click={() => (deletingPanelId = null)}>Keep panel</button>
									</div>
								</div>
							{/if}

							{#if editingPanelId === panel.id}
								<div class="panel-edit-form">
									{#each PANEL_FIELDS as [key, label, multiline]}
										<label>{label}:
											{#if multiline}
												<textarea rows="2" bind:value={editPanelForm[key]}></textarea>
											{:else}
												<input type="text" bind:value={editPanelForm[key]} />
											{/if}
										</label>
									{/each}
								</div>
							{:else}
								<div class="panel-content">
									{#each PANEL_FIELDS as [key, label]}
										<p class:prompt-text={key === 'image_prompt'}>
											<strong>{label}:</strong>
											<span class:narration={key === 'narration'}>{key === 'dialogue' ? `"${panel[key]}"` : panel[key]}</span>
										</p>
									{/each}
								</div>
							{/if}
						</div>
					{/each}

					{#if addingToChapterId === chapter.id}
						<div class="panel-card panel-add" role="group" aria-label="Add a panel to chapter {chapter.chapter_number}">
							<div class="panel-header-row"><span class="panel-num">New panel, chapter {chapter.chapter_number}</span></div>
							<div class="panel-edit-form">
								<label>Place on:
									<select bind:value={addPanelPageId}>
										{#each pagesOfChapter as pageOption (pageOption.id)}
											<option value={pageOption.id}>Page {pageOption.page_number}, after its {pageOption.panel_ids?.length ?? 0} panels</option>
										{/each}
										<option value={NEW_PAGE}>A new page at the end</option>
									</select>
								</label>
								{#each PANEL_FIELDS as [key, label, multiline]}
									<label>{label}:
										{#if multiline}
											<textarea rows="2" bind:value={addPanelForm[key]}></textarea>
										{:else}
											<input type="text" bind:value={addPanelForm[key]} />
										{/if}
									</label>
								{/each}
								<small>A scene is needed so the draft has something to draw. Other fields can stay empty.</small>
								<div class="edit-actions">
									<button class="primary-btn small" disabled={layoutBusy || !addPanelForm.scene_description.trim()} on:click={() => addPanel(chapter.id)}>Add panel</button>
									<button class="secondary-btn small" disabled={layoutBusy} on:click={() => (addingToChapterId = null)}>Cancel</button>
								</div>
							</div>
						</div>
					{:else}
						<button class="secondary-btn small add-panel-btn" disabled={layoutBusy} on:click={() => startAddPanel(chapter)}>
							<Icon name="plus" size={18} /> Add panel{chapters.length > 1 ? ` to chapter ${chapter.chapter_number}` : ''}
						</button>
					{/if}
				</div>
			{/each}

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
					<ApprovalTag state={draftStage} />
				</div>
			</header>

			{#if anyDraftStale}
				{@render staleBanner()}
			{/if}


			{#if pages.length === 0}<div class="stage-empty"><Icon name="draft" size={48} /></div>{/if}
			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card" class:card-stale={isApprovalStale(page.draft_approved_at, page.updated_at)}>
						<div class="card-title-row">
							<h3>#{page.page_number}</h3>
							{#if page.is_sample || page.sample_label}
								<BetaTag />
							{/if}
						</div>
						<div class="img-container" class:overlay-preview={page.lettering_mode === 'overlay'}>
							{#if page.draft_image_url && !failedImages.has(page.draft_image_url)}
								{#if page.lettering_mode === 'overlay'}
									<LetteringPreview session={letteringSession(page)} imageUrl={page.draft_image_url} imageAlt={`Draft ${page.page_number}`} onImageError={markImageFailed} />
								{:else}
									<img src={page.draft_image_url} alt="Draft {page.page_number}" on:error={markImageFailed} />
								{/if}
							{:else}
								<div class="empty-img"><Icon name="draft" size={48} /></div>
							{/if}
						</div>
						<div class="card-footer">
							<button class="secondary-btn" disabled={isJobActive} on:click={() => generateDraft(page.id)}>
								<Icon name="regenerate" size={18} /> Regen ({quoteLabel('draft_sketch', costQuotes)})
							</button>

							<ApprovalAction state={approvalState(page.draft_approved_at, page.updated_at)} onApprove={() => approveDraft(page.id)} />
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
					<select value={project.finish_preset || 'off'} on:change={changeFinishPreset} aria-label="Finish">
						<option value="off">Off</option>
						<option value="print">Print</option>
						<option value="print-plus">Plus</option>
						<option value="print-heavy">Heavy</option>
					</select>
				</label>
				<label class="finish-variant">
					<select bind:value={finishVariant} aria-label="Variant">
						<option value="painted">Painted</option>
						<option value="ink" disabled={allOverlay} title={allOverlay ? INK_OVERLAY_NOTE : undefined}>Ink</option>
					</select>
				</label>
				<div class="header-tags">
					<ApprovalTag state={finishStage} />
				</div>
			</header>

			{#if anyLineArtStale}
				{@render staleBanner()}
			{/if}
			{#if inkBlockedByOverlay}
				<div class="stale-stage-banner" role="status">
					<Icon name="info" size={18} /> {INK_OVERLAY_NOTE}
				</div>
			{/if}

			{#if pages.length === 0}<div class="stage-empty"><Icon name="draft" size={48} /></div>{/if}
			<div class="page-grid">
				{#each pages as page}
					<div class="page-preview-card" class:card-stale={isApprovalStale(finishApprovalAt(page), page.updated_at)}>
						<div class="card-title-row">
							<h3>#{page.page_number}</h3>
							{#if page.is_sample || page.sample_label}
								<BetaTag />
							{/if}
						</div>
						<div class="img-container" class:overlay-preview={page.lettering_mode === 'overlay'}>
							{#if (finishVariant === INK_VARIANT ? page.finished_ink_image_url : page.finished_image_url)}
								<button class="image-toggle" aria-label="Raw" on:click={() => rawPreviewByPage = { ...rawPreviewByPage, [page.id]: !rawPreviewByPage[page.id] }}>
									<Icon name="eye" size={18} />
								</button>
							{/if}
							{#if finishPreviewUrl(page) && !failedImages.has(finishPreviewUrl(page))}
								{#if page.lettering_mode === 'overlay' && finishVariant !== INK_VARIANT && !page.line_art_image_url && !page.line_art_url}
									<LetteringPreview session={letteringSession(page)} imageUrl={finishPreviewUrl(page)} imageAlt={`Page ${page.page_number}`} onImageError={markImageFailed} />
								{:else}
									<img src={finishPreviewUrl(page)} alt="Page {page.page_number}" on:error={markImageFailed} />
								{/if}
							{:else}
								<div class="empty-img"><Icon name="line_art" size={48} /></div>
							{/if}
						</div>
						<div class="card-footer">
							<button class="secondary-btn" aria-label="Regenerate" disabled={isJobActive || inkBlocked(page, finishVariant)} title={inkBlocked(page, finishVariant) ? INK_OVERLAY_NOTE : undefined} on:click={() => generateLineArt(page.id)}>
								<Icon name="regenerate" size={18} /> Regen ({quoteLabel('final_line_art', costQuotes)})
							</button>

							<ApprovalAction
								state={approvalState(finishApprovalAt(page), page.updated_at)}
								staleLabel="Re-approve"
								disabled={inkBlocked(page, finishVariant)}
								title={inkBlocked(page, finishVariant) ? INK_OVERLAY_NOTE : undefined}
								onApprove={() => approveLineArt(page.id)}
							/>
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
					<button class="secondary-btn" disabled={isJobActive} on:click={() => suggestNewPalette(page.id)}>
						<Icon name="colour" size={18} /> Palette ({quoteLabel('colour_suggestion', costQuotes)})
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
