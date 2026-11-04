<script>
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
		title: 'Untitled',
		art_style: 'cyberpunk-noir',
		chapter_count: 3,
		script: 'Two investigators uncover a conspiracy.'
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
			createError = (err && err.message) || 'Could not create project.';
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

	<header class="navbar">
		<div class="brand">
			<span class="logo">🎨</span>
			<div class="brand-text">
				<h1>Icomic</h1>
			</div>
		</div>

		<div class="nav-status">
			<div class="status-pill global-pill">
				<span class="indicator green"></span>
				<span>Global</span>
			</div>

			<div class="status-pill advisory-pill" title={AGE_TERMS_ADVISORY}>
				<span class="advisory-text">18+ Advisory</span>
			</div>

			{#if authMode === 'mock'}
				<div class="status-pill mock-pill">
					<span class="badge blue">Mock API</span>
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
				<span class="unauth-icon">🔒</span>
				<div class="unauth-text">
					<strong>Sign in:</strong> Enable <code>DEV_AUTH_ENABLED=true</code> and <code>PUBLIC_DEV_MODE=true</code>.
				</div>
			</div>
			<button class="enable-dev-btn" on:click={enableDevIdentity} aria-label="⚡ Enable dev identity">
				⚡ Enable dev identity
			</button>
		</div>
	{/if}

	<section class="project-bar" aria-label="Project">
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
							{p.title || 'Untitled'} ({p.art_style || 'standard'})
						</option>
					{/each}
				</select>
			{:else}
				<span class="no-projects-label">No projects</span>
			{/if}
		</div>

		{#if activeProject}
			<div class="project-meta-pill">
				<span class="meta-item"><strong>Style:</strong> {activeProject.art_style || 'classic'}</span>
				<span class="meta-divider">•</span>
				<span class="meta-item"><strong>Chapters:</strong> {activeProject.chapter_count || 1}</span>
			</div>
		{/if}

		<div class="project-actions">
			<button class="action-btn create-btn" on:click={openCreateModal}>
				➕ New
			</button>

			{#if activeProject}
				<button
					class="action-btn delete-btn"
					on:click={handleDeleteCurrentProject}
				>
					🗑️ Delete
				</button>
			{/if}

			<button
				class="action-btn refresh-btn"
				on:click={loadProjects}
			>
				🔄 Refresh
			</button>
		</div>
	</section>

	{#if projectError}
		<div class="project-error-banner">
			<strong>⚠️ {projectError.title}:</strong> {projectError.message}
			{#if projectError.action}
				<span class="error-action-hint">({projectError.action})</span>
			{/if}
		</div>
	{/if}

	<main class="main-container">
		{#if isLoadingProjects}
			<div class="loading-container">
				<div class="spinner-large"></div>
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
				<div class="empty-icon">📖</div>
				<button class="primary-btn large" on:click={openCreateModal}>
					🚀 Create project
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
				role="dialog"
				aria-modal="true"
				aria-labelledby="modal-title"
				tabindex="-1"
			>
				<header class="modal-header">
					<h3 id="modal-title">New project</h3>
					<button class="close-btn" on:click={closeCreateModal} aria-label="Close dialog">✕</button>
				</header>

				{#if createError}
					<div class="modal-error">
						⚠️ {createError}
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
								max="10"
								bind:value={newProjectForm.chapter_count}
							/>
						</div>
					</div>

					<div class="form-group">
						<label for="new-proj-script">Script *</label>
						<textarea
							id="new-proj-script"
							rows="5"
							placeholder="Story…"
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
							aria-label="Create"
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

<style>
	:global(body) {
		margin: 0;
		padding: 0;
		font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
		background: #f1f5f9;
		color: #0f172a;
	}
	.app-layout {
		min-height: 100vh;
		display: flex;
		flex-direction: column;
	}
	.navbar {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 0.75rem 2rem;
		background: #ffffff;
		border-bottom: 1px solid #e2e8f0;
		box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.logo {
		font-size: 1.75rem;
	}
	.brand-text h1 {
		margin: 0;
		font-size: 1.25rem;
		font-weight: 800;
		color: #0f172a;
		letter-spacing: -0.025em;
	}
	.nav-status {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.status-pill {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		background: #f8fafc;
		border: 1px solid #e2e8f0;
		padding: 0.3rem 0.6rem;
		border-radius: 9999px;
		font-size: 0.75rem;
		font-weight: 500;
		color: #334155;
	}
	.indicator {
		width: 6px;
		height: 6px;
		border-radius: 50%;
	}
	.indicator.green {
		background: #16a34a;
	}
	.indicator.amber {
		background: #d97706;
	}
	.advisory-text {
		color: #64748b;
	}
	.badge.blue {
		background: #0284c7;
		color: #ffffff;
		padding: 0.1rem 0.4rem;
		border-radius: 4px;
		font-size: 0.7rem;
		font-weight: 600;
	}
	.unauth-pill {
		background: #fffbeb;
		border-color: #fde68a;
		color: #92400e;
	}
	.dev-pill {
		background: #f0fdf4;
		border-color: #bbf7d0;
		color: #166534;
	}

	.unauth-banner {
		background: #fffbeb;
		border-bottom: 1px solid #fde68a;
		padding: 0.6rem 2rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 1rem;
		font-size: 0.8125rem;
		color: #92400e;
	}
	.unauth-content {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.unauth-icon {
		font-size: 1.1rem;
	}
	.unauth-text code {
		background: rgba(0, 0, 0, 0.06);
		padding: 0.1rem 0.3rem;
		border-radius: 3px;
		font-size: 0.75rem;
	}
	.enable-dev-btn {
		background: #0284c7;
		color: white;
		border: none;
		padding: 0.35rem 0.75rem;
		border-radius: 6px;
		font-size: 0.75rem;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}
	.enable-dev-btn:hover {
		background: #0369a1;
	}

	.project-bar {
		background: #ffffff;
		border-bottom: 1px solid #e2e8f0;
		padding: 0.6rem 2rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		flex-wrap: wrap;
	}
	.project-selector-group {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.selector-label {
		font-size: 0.8125rem;
		font-weight: 700;
		color: #475569;
	}
	.project-select {
		padding: 0.4rem 0.75rem;
		border: 1px solid #cbd5e1;
		border-radius: 6px;
		font-size: 0.8125rem;
		font-weight: 600;
		color: #0f172a;
		background: #ffffff;
		cursor: pointer;
	}
	@media (max-width: 600px) {
		.project-bar {
			padding-inline: 1rem;
		}
		.project-selector-group {
			flex: 1 1 100%;
			min-width: 0;
		}
		.project-select {
			flex: 1;
			min-width: 0;
		}
	}
	.loading-label,
	.no-projects-label {
		font-size: 0.8125rem;
		color: #64748b;
		font-style: italic;
	}
	.project-meta-pill {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.75rem;
		color: #64748b;
		background: #f8fafc;
		border: 1px solid #e2e8f0;
		padding: 0.25rem 0.6rem;
		border-radius: 6px;
	}
	.meta-divider {
		color: #cbd5e1;
	}
	.project-actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.action-btn {
		font-size: 0.75rem;
		font-weight: 600;
		padding: 0.35rem 0.7rem;
		border-radius: 6px;
		cursor: pointer;
		border: 1px solid #cbd5e1;
		background: #ffffff;
		color: #334155;
		transition: all 0.15s ease;
	}
	.action-btn:hover {
		background: #f1f5f9;
	}
	.create-btn {
		background: #0284c7;
		color: #ffffff;
		border-color: #0284c7;
	}
	.create-btn:hover {
		background: #0369a1;
	}
	.delete-btn {
		color: #b91c1c;
		border-color: #fca5a5;
	}
	.delete-btn:hover {
		background: #fee2e2;
	}

	.project-error-banner {
		background: #fef2f2;
		color: #991b1b;
		border-bottom: 1px solid #fecaca;
		padding: 0.6rem 2rem;
		font-size: 0.8125rem;
	}
	.error-action-hint {
		margin-left: 0.3rem;
		color: #7f1d1d;
	}

	.main-container {
		flex: 1;
		max-width: 1100px;
		width: 100%;
		margin: 1.5rem auto;
		padding: 0 1rem;
		box-sizing: border-box;
	}

	.loading-container {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: 4rem 2rem;
		color: #64748b;
		gap: 1rem;
	}
	.spinner-large {
		width: 32px;
		height: 32px;
		border: 3px solid #0284c7;
		border-radius: 50%;
		border-top-color: transparent;
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	.empty-state-card {
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 3rem 2rem;
		text-align: center;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 1rem;
		max-width: 500px;
		margin: 2rem auto;
	}
	.empty-icon {
		font-size: 3rem;
	}
	.primary-btn {
		background: #0284c7;
		color: white;
		border: none;
		padding: 0.5rem 1rem;
		border-radius: 6px;
		font-weight: 600;
		font-size: 0.875rem;
		cursor: pointer;
	}
	.primary-btn.large {
		padding: 0.75rem 1.5rem;
		font-size: 0.9375rem;
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
		padding: 0.5rem 1rem;
		border-radius: 6px;
		font-size: 0.875rem;
		font-weight: 500;
		cursor: pointer;
	}

	.modal-backdrop {
		position: fixed;
		top: 0;
		left: 0;
		right: 0;
		bottom: 0;
		background: rgba(15, 23, 42, 0.45);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 1000;
		padding: 1rem;
	}
	.modal-dialog {
		background: #ffffff;
		border-radius: 8px;
		box-shadow: 0 10px 25px rgba(0, 0, 0, 0.15);
		width: 100%;
		max-width: 520px;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}
	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 1rem 1.25rem;
		border-bottom: 1px solid #e2e8f0;
	}
	.modal-header h3 {
		margin: 0;
		font-size: 1.1rem;
		color: #0f172a;
	}
	.close-btn {
		background: transparent;
		border: none;
		font-size: 1rem;
		color: #64748b;
		cursor: pointer;
		padding: 0.2rem 0.4rem;
	}
	.modal-error {
		background: #fef2f2;
		color: #991b1b;
		border-bottom: 1px solid #fecaca;
		padding: 0.5rem 1.25rem;
		font-size: 0.8125rem;
	}
	.modal-form {
		padding: 1.25rem;
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}
	.form-row {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1rem;
	}
	.form-group {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}
	.form-group label {
		font-size: 0.8125rem;
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
	.modal-footer {
		display: flex;
		justify-content: flex-end;
		gap: 0.5rem;
		padding-top: 0.5rem;
		border-top: 1px solid #f1f5f9;
	}
</style>
