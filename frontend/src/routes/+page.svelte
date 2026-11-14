<script>
	import '#lib/design/theme.css';
	import ComicEditor from '#lib/editor/ComicEditor.svelte';
	import ScriptImport from '#lib/editor/ScriptImport.svelte';
	import { Icon, focusDialog, AppHeader, AppFooter, Hamburger, Sidebar, StageNav, SpendPill, stageList } from '#lib/design/shell.js';
	import { formatApiError, publishSpend, spendStore, spendView } from '#lib/api/index.js';
	import { AGE_TERMS_ADVISORY } from '#lib/config/policies.js';
	import { sharedClient, projectAccess, LOGIN_LIMIT_URL } from '#lib/account/session.js';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	let apiClient = sharedClient();

	let authMode = apiClient.getAuthMode();
	let account = null;
	$: access = projectAccess(account);

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
	let stageCompleted = {};
	let editor;
	let sidebar;
	let sidebarOpen = false;
	let hamburger = null;

	function trackProgress(completed) {
		stageCompleted = completed;
	}

	function toggleSidebar() {
		sidebar.toggle();
	}

	function selectStage(event) {
		if (editor) editor.selectStage(event.detail);
		else activeStage = event.detail;
	}

	async function loadProjects() {
		isLoadingProjects = true;
		projectError = null;
		authMode = apiClient.getAuthMode();

		try {
			account = await apiClient.getAccount();
			if (account) apiClient.getCreditUsage().then(publishSpend, () => publishSpend(null));
			const res = account ? await apiClient.listProjects() : [];
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
		if (access.needsLogin) {
			goto(LOGIN_LIMIT_URL);
			return;
		}
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
		// The script is stored as written, so an imported file keeps its own spacing.
		const script = newProjectForm.script;

		if (!title) {
			createError = 'Enter title.';
			return;
		}
		if (!script.trim()) {
			createError = 'Add script.';
			return;
		}

		isCreatingProject = true;
		createError = null;

		try {
			await apiClient.ensureSession();
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
			if (err?.code === 'project_limit_reached' && err.details?.account_kind !== 'registered') {
				goto(LOGIN_LIMIT_URL);
				return;
			}
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

	async function handleSignOut() {
		try {
			await apiClient.signOut();
		} catch (err) {
			projectError = formatApiError(err);
			return;
		}
		activeProjectId = '';
		await loadProjects();
	}

	onMount(() => {
		loadProjects();
	});
</script>

<div class="app-layout">

	<AppHeader>
		<svelte:fragment slot="leading">
			<Hamburger bind:button={hamburger} open={sidebarOpen} controls="app-sidebar" on:click={toggleSidebar} />
		</svelte:fragment>

		<svelte:fragment slot="status">
			<div class="nav-status">
				<div class="status-pill global-pill">
					<Icon name="globe" size={14} /><span class="pill-text">Global</span>
				</div>

				<div class="status-pill advisory-pill" title={AGE_TERMS_ADVISORY}>
					<Icon name="lock" size={14} /><span class="pill-text">18+ Advisory</span>
				</div>
				<SpendPill view={spendView($spendStore)} />
			</div>
		</svelte:fragment>

		<svelte:fragment slot="account">
			{#if authMode === 'dev'}
				<div class="status-pill dev-pill">
					<span class="indicator green"></span>
					<span class="dev-label">Dev</span>
				</div>
			{:else if authMode === 'bearer'}
				<div class="status-pill bearer-pill">
					<span class="indicator green"></span>
					<span>Token</span>
				</div>
			{/if}

			{#if account?.email}
				<div class="account-control">
					<span class="status-pill">{account.email}</span>
					<button class="action-btn" on:click={handleSignOut}>Sign out</button>
				</div>
			{:else if account?.kind !== 'registered'}
				<a class="account-link" href="/login">Sign in</a>
			{/if}
		</svelte:fragment>
	</AppHeader>

	<Sidebar bind:this={sidebar} bind:open={sidebarOpen} id="app-sidebar" label="Menu" trigger={hamburger} disabled={showCreateModal}>
		<StageNav stages={stageList} active={activeStage} completed={stageCompleted} disabled={!activeProject} on:select={selectStage} />
	</Sidebar>

	<section class="project-bar" aria-label="Project" aria-busy={isLoadingProjects}>
		<div class="project-selector-group">
			<label for="project-select" class="selector-label">Project:</label>
			{#if isLoadingProjects}
				<span class="loading-label">Loading…</span>
			{:else if projects.length > 0 && !access.canSeeList}
				<span class="project-title">{activeProject?.title || 'Untitled'}</span>
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
				<span class="no-projects-label">None</span>
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
		<div class="demo-sample-banner" role="status">
			<span class="sample-banner-icon"><Icon name="check" size={18} /></span>
			<div class="sample-banner-body">
				Meta AI sample. No charges.
			</div>
		</div>
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
				<img class="brand-loader" src="/brand/mark.svg" alt="" />
				<p>Loading…</p>
			</div>
		{:else if activeProject}
			<ComicEditor
				bind:this={editor}
				project={activeProject}
				{apiClient}
				bind:activeStage
				onProgress={trackProgress}
			/>
		{:else}
			<div class="empty-state-card">
				<div class="empty-icon"><img src="/brand/mark.svg" alt="" /></div>
				<button class="primary-btn large" on:click={openCreateModal}>
					<Icon name="export" size={18} /> Create project
				</button>
			</div>
		{/if}
	</main>

	<AppFooter />

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
					<button class="close-btn" on:click={closeCreateModal} aria-label="Close"><Icon name="close" size={18} /></button>
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
							<option value="manga-ink">Shonen</option>
							<option value="indie-graphic-novel">Indie</option>
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
						<ScriptImport current={newProjectForm.script} disabled={isCreatingProject} inputId="new-proj-script-file" onImport={(text) => (newProjectForm.script = text)} />
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
