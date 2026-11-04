<script>
	import { pollJob } from '$lib/api/jobPolling.js';
	import { EXPORT_FORMATS, PAGE_SIZES } from '$lib/api/types.js';

	export let projectId = '';
	export let apiClient;
	export let pages = [];

	let format = EXPORT_FORMATS.PDF;
	let pageSize = PAGE_SIZES.A4;
	let isExporting = false;
	let exportError = '';
	let currentExport = null;
	let downloadUrl = '';

	async function handleExport() {
		if (!apiClient || !projectId) return;
		isExporting = true;
		exportError = '';
		currentExport = null;
		downloadUrl = '';

		try {
			const res = await apiClient.exportProject(projectId, {
				format,
				page_size: pageSize
			});

			if (res.job) {
				await pollJob(apiClient, res.job.id, {
					intervalMs: 500,
					timeoutMs: 30000
				});
			}

			if (res.export) {
				currentExport = res.export;
				downloadUrl = res.export.download_url || apiClient.getExportDownloadUrl(res.export.id);
			}
		} catch (err) {
			exportError = err.message || 'Export failed';
		} finally {
			isExporting = false;
		}
	}
</script>

<div class="export-view">
	<header class="export-header">
		<h3>Export</h3>

	</header>

	<div class="compliance-notice">
		<div class="badge">ℹ️ AI disclosure</div>
		<p>
			AI-generated with Meta AI (muse-image-1.0, Llama). C2PA included.
		</p>
	</div>

	<div class="options-grid">
		<div class="option-card">
			<label for="format-select">Format</label>
			<select id="format-select" bind:value={format} aria-label="Export Format">
				<option value={EXPORT_FORMATS.PDF}>PDF</option>
				<option value={EXPORT_FORMATS.PNG}>PNG (.zip)</option>
			</select>
			<small>PDF has cover, panels, dialogue, and page numbers.</small>
		</div>

		<div class="option-card">
			<label for="pagesize-select">Size</label>
			<select id="pagesize-select" bind:value={pageSize} aria-label="Page Size">
				<option value={PAGE_SIZES.A4}>A4 (210 × 297 mm)</option>
				<option value={PAGE_SIZES.US_LETTER}>US Letter (8.5 × 11 in)</option>
				<option value={PAGE_SIZES.CUSTOM}>Custom (6.625 × 10.25 in)</option>
			</select>
			<small>Print-ready sizes.</small>
		</div>
	</div>

	<div class="preview-section">
		<h4>Preview ({pages.length})</h4>
		<div class="pages-strip">
			{#if pages.length === 0}
				<div class="empty-pages">No pages yet.</div>
			{:else}
				{#each pages as page, i}
					<div class="page-card">
						<div class="page-preview-box">
							<span class="page-num">Page {i + 1}</span>
							<span class="page-status">
								{page.colour_approved_at ? '🎨 Coloured' : page.line_art_approved_at ? '✒️ Line Art' : 'Draft'}
							</span>
						</div>
					</div>
				{/each}
			{/if}
		</div>
	</div>

	{#if exportError}
		<div class="error-banner">
			⚠️ {exportError}
		</div>
	{/if}

	<div class="export-actions">
		<button
			class="export-btn"
			aria-label={isExporting ? `Assembling ${format.toUpperCase()}...` : `🚀 Generate & Assemble Comic ${format.toUpperCase()}`}
			on:click={handleExport}
			disabled={isExporting}
		>
			{#if isExporting}
				<span class="spinner"></span> Assembling {format.toUpperCase()}...
			{:else}
				🚀 Export {format.toUpperCase()}
			{/if}
		</button>

		{#if downloadUrl}
			<a class="download-link" href={downloadUrl} target="_blank" rel="noopener noreferrer" download>
				📥 Download {format.toUpperCase()} Comic
			</a>
		{/if}
	</div>
</div>

<style>
	.export-view {
		display: flex;
		flex-direction: column;
		gap: 1.5rem;
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1.5rem;
	}
	.export-header h3 {
		margin: 0 0 0.25rem 0;
		font-size: 1.25rem;
		color: #0f172a;
	}
	.compliance-notice {
		background: #f8fafc;
		border-left: 4px solid #0284c7;
		padding: 0.75rem 1rem;
		border-radius: 4px;
		font-size: 0.8125rem;
		color: #334155;
	}
	.compliance-notice p {
		margin: 0.25rem 0 0 0;
		line-height: 1.4;
	}
	.badge {
		font-size: 0.75rem;
		font-weight: 600;
		color: #0284c7;
		text-transform: uppercase;
	}
	.options-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
		gap: 1rem;
	}
	.option-card {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
		padding: 1rem;
		background: #f8fafc;
		border: 1px solid #e2e8f0;
		border-radius: 6px;
	}
	.option-card label {
		font-size: 0.875rem;
		font-weight: 600;
		color: #1e293b;
	}
	.option-card select {
		padding: 0.5rem;
		border: 1px solid #cbd5e1;
		border-radius: 4px;
		background: #ffffff;
		font-size: 0.875rem;
	}
	.option-card small {
		color: #64748b;
		font-size: 0.75rem;
	}
	.preview-section h4 {
		margin: 0 0 0.75rem 0;
		font-size: 0.95rem;
		color: #1e293b;
	}
	.pages-strip {
		display: flex;
		gap: 0.75rem;
		overflow-x: auto;
		padding-bottom: 0.5rem;
	}
	.page-card {
		flex: 0 0 120px;
		height: 160px;
		border: 1px solid #cbd5e1;
		border-radius: 6px;
		background: #ffffff;
		display: flex;
		align-items: center;
		justify-content: center;
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
	}
	.page-preview-box {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.5rem;
	}
	.page-num {
		font-size: 0.875rem;
		font-weight: 600;
		color: #0f172a;
	}
	.page-status {
		font-size: 0.75rem;
		color: #64748b;
	}
	.empty-pages {
		color: #94a3b8;
		font-size: 0.875rem;
		padding: 1rem;
	}
	.error-banner {
		background: #fef2f2;
		color: #991b1b;
		padding: 0.75rem;
		border-radius: 6px;
		font-size: 0.875rem;
		border: 1px solid #fecaca;
	}
	.export-actions {
		display: flex;
		align-items: center;
		gap: 1rem;
		padding-top: 0.5rem;
	}
	.export-btn {
		background: #0284c7;
		color: #ffffff;
		border: none;
		padding: 0.6rem 1.25rem;
		border-radius: 6px;
		font-size: 0.95rem;
		font-weight: 600;
		cursor: pointer;
		display: flex;
		align-items: center;
		gap: 0.5rem;
		transition: background 0.15s ease;
	}
	.export-btn:hover:not(:disabled) {
		background: #0369a1;
	}
	.export-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}
	.download-link {
		background: #16a34a;
		color: #ffffff;
		text-decoration: none;
		padding: 0.6rem 1.25rem;
		border-radius: 6px;
		font-size: 0.95rem;
		font-weight: 600;
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		transition: background 0.15s ease;
	}
	.download-link:hover {
		background: #15803d;
	}
	.spinner {
		display: inline-block;
		width: 14px;
		height: 14px;
		border: 2px solid #ffffff;
		border-radius: 50%;
		border-top-color: transparent;
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
