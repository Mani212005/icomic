<script>
	import '$lib/design/theme.css';
	import Paper from '$lib/design/Paper.svelte';
	import { focusDialog } from '$lib/design/dialog.js';
	import Icon from '$lib/design/Icon.svelte';
	import ComicEditor from '$lib/editor/ComicEditor.svelte';
	import { createDefaultClient, formatApiError } from '$lib/api/index.js';
	import { AGE_TERMS_ADVISORY } from '$lib/config/policies.js';
	import { onMount } from 'svelte';

	let apiClient = createDefaultClient();

	let isAuthenticated = apiClient.isAuthenticated();
	let authMode = apiClient.getAuthMode();

	let projects = [];
	let activeProjectId = '';
	let activeProject = null;
	let isLoadingProjects = true;
	let projectError = null;

	let showCreateModal = false;
	let isCreatingProject = false;
	let createError = null;
	let newProjectForm = {
		title: '',
		art_style: 'cyberpunk-noir',
		chapter_count: 3,
		script: ''
	};

	let activeStage = 'script';

	async function loadProjects() {
		isLoadingProjects = true;
		projectError = null;
		isAuthenticated = apiClient.isAuthenticated();
		authMode = apiClient.getAuthMode();

		try {
			const res = await apiClient.listProjects();
			const list = Array.isArray(res) ? res : (res && res.projects ? res.projects : []);
			projects = list;

			if (projects.length > 0) {
				const existing = projects.find((p) => p.id === activeProjectId);
				if (existing) {
					activeProject = existing;
				} else {
					activeProjectId = projects[0].id;
					activeProject = projects[0];
				}
			} else {
				activeProjectId = '';
				activeProject = null;
			}
		} catch (err) {
			console.error('Projects:', err);
			projectError = formatApiError(err);
		} finally {
			isLoadingProjects = false;
		}
	}

	function switchProject(id) {
		const target = projects.find((p) => p.id === id);
		if (target) {
			activeProjectId = target.id;
			activeProject = target;
			activeStage = 'script';
		}
	}

	function openCreateModal() {
		newProjectForm = {
			title: '',
			art_style: 'cyberpunk-noir',
			chapter_count: 3,
			script: ''
		};
		createError = null;
		showCreateModal = true;
	}

	function closeCreateModal() {
		showCreateModal = false;
		createError = null;
	}

	async function handleCreateProject() {
		const title = newProjectForm.title.trim();
		const script = newProjectForm.script.trim();

		if (!title) {
			createError = 'Enter title.';
			return;
		}
		if (!script) {
			createError = 'Add script.';
			return;
		}

		isCreatingProject = true;
		createError = null;

		try {
			const created = await apiClient.createProject({
				title,
				art_style: newProjectForm.art_style,
				chapter_count: Number(newProjectForm.chapter_count) || 1,
				original_script: script,
				script
			});

			showCreateModal = false;
			await loadProjects();
			if (created && created.id) {
				switchProject(created.id);
			}
		} catch (err) {
			createError = (err && err.message) || 'Creation failed.';
		} finally {
			isCreatingProject = false;
		}
	}

	async function handleDeleteCurrentProject() {
		if (!activeProject || !activeProject.id) return;
		if (!confirm(`Delete project "${activeProject.title}"?`)) return;

		try {
			await apiClient.deleteProject(activeProject.id);
			await loadProjects();
		} catch (err) {
			projectError = formatApiError(err);
		}
	}

	function enableDevIdentity() {
		apiClient.setDevMode(true, '00000000-0000-0000-0000-000000000001');
		isAuthenticated = apiClient.isAuthenticated();
		authMode = apiClient.getAuthMode();
		loadProjects();
	}

	onMount(() => {
		loadProjects();
	});
</script>

<div class="app-layout">
	<Paper />

	<header class="navbar">
		<div class="brand">
			<span class="logo"><Icon name="panels" size={30} /></span>
			<div class="brand-text">
				<h1>Icomic</h1>
			</div>
		</div>

		<div class="nav-status">
			<div class="status-pill global-pill">

				<Icon name="globe" size={14} /><span>Global</span>
			</div>

			<div class="status-pill advisory-pill" title={AGE_TERMS_ADVISORY}>
				<Icon name="lock" size={14} /><span class="advisory-text">18+ Advisory</span>
			</div>

			{#if authMode === 'mock'}
				<div class="status-pill mock-pill" title="Interactive demo previewing real model output">
					<Icon name="mock" size={14} /><span class="badge blue">Sample Demo</span>
				</div>
			{:else if authMode === 'dev'}
				<div class="status-pill dev-pill">
					<span class="indicator green"></span>
					<span class="dev-label">Dev</span>
				</div>
			{:else if authMode === 'bearer'}
				<div class="status-pill bearer-pill">
					<span class="indicator green"></span>
					<span>Token</span>
				</div>
			{:else}
				<div class="status-pill unauth-pill">
					<span class="indicator amber"></span>
					<span class="unauth-label">Signed out</span>
				</div>
			{/if}
		</div>
	</header>

	{#if !isAuthenticated && authMode !== 'mock'}
		<div class="unauth-banner" role="alert">
			<div class="unauth-content">
				<span class="unauth-icon"><Icon name="lock" size={18} /></span>
				<div class="unauth-text">
					<strong>Sign in:</strong> Enable <code>DEV_AUTH_ENABLED=true</code> and <code>PUBLIC_DEV_MODE=true</code>.
				</div>
			</div>
			<button class="enable-dev-btn" on:click={enableDevIdentity} >
				<Icon name="script" size={18} /> Enable dev identity
			</button>
		</div>
	{/if}

	<section class="project-bar" aria-label="Project" aria-busy={isLoadingProjects}>
		<div class="project-selector-group">
			<label for="project-select" class="selector-label">Project:</label>
			{#if isLoadingProjects}
				<span class="loading-label">Loading…</span>
			{:else if projects.length > 0}
				<select
					id="project-select"
					class="project-select"
					bind:value={activeProjectId}
					on:change={() => switchProject(activeProjectId)}
				>
					{#each projects as p}
						<option value={p.id}>
							{p.title || 'Untitled'}
						</option>
					{/each}
				</select>
			{:else}
				<span class="no-projects-label">No projects</span>
			{/if}
		</div>

		{#if isLoadingProjects}
			<div class="project-meta-pill loading-meta" aria-hidden="true"></div>
		{:else if activeProject}
			<div class="project-meta-pill">
				<span class="meta-item"><strong>Style:</strong> {activeProject.art_style || 'classic'}</span>
				<span class="meta-divider">•</span>
				<span class="meta-item"><strong>Chapters:</strong> {activeProject.chapter_count || 1}</span>
			</div>
		{/if}

		<div class="project-actions">
			<button class="action-btn create-btn" on:click={openCreateModal}>
				<Icon name="plus" size={18} /> New
			</button>

			{#if isLoadingProjects}
				<button class="action-btn loading-action" aria-hidden="true" tabindex="-1" disabled><Icon name="delete" size={18} /></button>
			{:else if activeProject}
				<button
					class="action-btn delete-btn"
					on:click={handleDeleteCurrentProject}
				>
					<Icon name="delete" size={18} /> Delete
				</button>
			{/if}

			<button
				class="action-btn refresh-btn"
				on:click={loadProjects}
			>
				<Icon name="refresh" size={18} /> Refresh
			</button>
		</div>
	</section>

	{#if authMode === 'mock'}
		<aside class="demo-sample-banner" role="status" aria-label="Interactive Demo Notice">
			<span class="sample-banner-icon"><Icon name="check" size={18} /></span>
			<div class="sample-banner-body">
				<strong>Interactive Sample Demo:</strong>
				Showing approved real Meta AI model output from our publication pipeline. Fully interactive stage previews with zero API key or credit requirements.
			</div>
		</aside>
	{/if}

	{#if projectError}
		<div class="project-error-banner" role="alert">
			<strong><Icon name="warning" size={18} /> {projectError.title}:</strong> {projectError.message}
			{#if projectError.action}
				<span class="error-action-hint">({projectError.action})</span>
			{/if}
		</div>
	{/if}

	<main class="main-container">
		{#if isLoadingProjects}
			<div class="loading-container" role="status">
				<div class="spinner-large" aria-hidden="true"></div>
				<p>Loading…</p>
			</div>
		{:else if activeProject}
			<ComicEditor
				project={activeProject}
				{apiClient}
				bind:activeStage
			/>
		{:else}
			<div class="empty-state-card">
				<div class="empty-icon"><Icon name="panels" size={18} /></div>
				<button class="primary-btn large" on:click={openCreateModal}>
					<Icon name="export" size={18} /> Create project
				</button>
			</div>
		{/if}
	</main>

	{#if showCreateModal}
		<div
			class="modal-backdrop"
			on:click={(e) => e.target === e.currentTarget && closeCreateModal()}
			on:keydown={(e) => e.key === 'Escape' && closeCreateModal()}
			role="presentation"
		>
			<div
				class="modal-dialog"
				use:focusDialog={closeCreateModal}
				role="dialog"
				aria-modal="true"
				aria-labelledby="modal-title"
				tabindex="-1"
			>
				<header class="modal-header">
					<h2 id="modal-title">New project</h2>
					<button class="close-btn" on:click={closeCreateModal} aria-label="Close dialog"><Icon name="close" size={18} /></button>
				</header>

				{#if createError}
					<div class="modal-error" role="alert">
						<Icon name="warning" size={18} /> {createError}
					</div>
				{/if}

				<form class="modal-form" on:submit|preventDefault={handleCreateProject}>
					<div class="form-group">
						<label for="new-proj-title">Title *</label>
						<input
							id="new-proj-title"
							type="text"
							bind:value={newProjectForm.title}
							required
						/>
					</div>

					<div class="form-row">
						<div class="form-group">
						<label for="new-proj-style">Style</label>
							<select id="new-proj-style" bind:value={newProjectForm.art_style}>
							<option value="cyberpunk-noir">Cyberpunk</option>
							<option value="classic-superhero">Superhero</option>
							<option value="manga-ink">Shonen ink</option>
							<option value="indie-graphic-novel">Indie novel</option>
							</select>
						</div>

						<div class="form-group">
							<label for="new-proj-chapters">Chapters</label>
							<input
								id="new-proj-chapters"
								type="number"
								min="1"
								bind:value={newProjectForm.chapter_count}
							/>
						</div>
					</div>

					<div class="form-group">
						<label for="new-proj-script">Script *</label>
						<textarea
							id="new-proj-script"
							rows="5"

							bind:value={newProjectForm.script}
							required
						></textarea>
					</div>

					<footer class="modal-footer">
						<button
							type="button"
							class="secondary-btn"
							on:click={closeCreateModal}
							disabled={isCreatingProject}
						>
							Cancel
						</button>
						<button
							type="submit"
							class="primary-btn"
							disabled={isCreatingProject}
						>
							{isCreatingProject ? 'Creating…' : 'Create'}
						</button>
					</footer>
				</form>
			</div>
		</div>
	{/if}
</div>
