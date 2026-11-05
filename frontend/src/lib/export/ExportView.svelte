<script>
	import Icon from '#lib/design/Icon.svelte';
	import { pollJob } from '#lib/api/jobPolling.js';
	import { EXPORT_FORMATS, PAGE_SIZES, formatApiError } from '#lib/api/types.js';

	export let projectId = '';
	export let apiClient;
	export let pages = [];
	export let onComplete = () => {};

	let format = EXPORT_FORMATS.PDF;
	let pageSize = PAGE_SIZES.A4;
	let isExporting = false;
	let exportError = null;
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
				onComplete();
			}
		} catch (err) {
			exportError = formatApiError(err);
		} finally {
			isExporting = false;
		}
	}
</script>

<div class="export-view">
	<header class="export-header">
		<h2>Export</h2>

	</header>

	<div class="compliance-notice">
		<div class="badge"><Icon name="info" size={18} /> AI disclosure</div>
			<p>AI-generated: Meta AI (muse-image-1.0, Llama); C2PA included.</p>
	</div>

	<div class="options-grid">
		<div class="option-card">
			<label for="format-select">Format</label>
			<select id="format-select" bind:value={format}>
				<option value={EXPORT_FORMATS.PDF}>PDF</option>
				<option value={EXPORT_FORMATS.PNG}>PNG (.zip)</option>
			</select>

		</div>

		<div class="option-card">
			<label for="pagesize-select">Size</label>
			<select id="pagesize-select" bind:value={pageSize}>
				<option value={PAGE_SIZES.A4}>A4 (210 × 297 mm)</option>
				<option value={PAGE_SIZES.US_LETTER}>US Letter (8.5 × 11 in)</option>
				<option value={PAGE_SIZES.CUSTOM}>Custom (6.625 × 10.25 in)</option>
			</select>
		</div>
	</div>

	<div class="preview-section">
		<h3>Pages ({pages.length})</h3>
		<div class="pages-strip">
			{#if pages.length === 0}
				<div class="empty-pages"><Icon name="panels" size={40} />None.</div>
			{:else}
				{#each pages as page, i}
					<div class="page-card">
						<div class="page-preview-box">
							<span class="page-num">Page {i + 1}</span>
							<span class="page-status"><Icon name={page.colour_approved_at ? 'colour' : page.line_art_approved_at ? 'line_art' : 'draft'} size={16} />
								{page.colour_approved_at ? 'Coloured' : page.line_art_approved_at ? 'Lines' : 'Draft'}
							</span>
						</div>
					</div>
				{/each}
			{/if}
		</div>
	</div>

	{#if exportError}
		<div class="error-banner" role="alert">
			<Icon name="warning" size={18} />
			<div>
				<strong>{exportError.title}</strong>
				<p>{exportError.message}</p>
				{#if exportError.action}<p class="error-action">Next: {exportError.action}</p>{/if}
			</div>
		</div>
	{/if}

	<div class="export-actions">
		<button
			class="export-btn"
			aria-label={isExporting ? `Exporting ${format.toUpperCase()}` : `Export ${format.toUpperCase()}`}
			on:click={handleExport}
			disabled={isExporting}
			aria-busy={isExporting}
		>
			{#if isExporting}
				<span class="spinner"></span> Exporting {format.toUpperCase()}...
			{:else}
				<Icon name="export" size={18} /> Export {format.toUpperCase()}
			{/if}
		</button>

		{#if downloadUrl}
			<a class="download-link" href={downloadUrl} target="_blank" rel="noopener noreferrer" download>
				<Icon name="download" size={18} /> Download {format.toUpperCase()}
			</a>
		{/if}
	</div>
</div>
