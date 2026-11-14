<script>
	import { SCRIPT_FILE_ACCEPT, readScriptFile } from './scriptImport.js';
	import { Icon } from '#lib/design/shell.js';

	// Reads a TXT or Markdown file and hands its text to onImport. When the script already has text the
	// import waits for a confirmation, because it replaces that text.
	export let current = '';
	export let disabled = false;
	export let onImport = () => {};
	export let inputId = 'script-file';

	let problem = '';
	let imported = null;
	let waiting = null;
	let input;

	async function choose(event) {
		const file = event.currentTarget.files?.[0];
		event.currentTarget.value = '';
		problem = '';
		imported = null;
		waiting = null;
		if (!file) return;
		try {
			const read = await readScriptFile(file);
			if (current.trim() && current !== read.text) waiting = read;
			else accept(read);
		} catch (error) {
			problem = error?.message || 'The file could not be read.';
		}
	}

	function accept(read) {
		waiting = null;
		onImport(read.text, read);
		imported = read;
	}
</script>

<div class="script-import">
	<input id={inputId} class="file-input" type="file" accept={SCRIPT_FILE_ACCEPT} {disabled} aria-label="Script file" on:change={choose} />
	<label class="secondary-btn small file-button" class:disabled for={inputId}>
		<Icon name="upload" size={18} /> Import TXT or Markdown
	</label>

	{#if waiting}
		<div class="import-confirm" role="alertdialog" aria-label="Confirm replace">
			<p><Icon name="warning" size={18} /> Replace the script with {waiting.name}?</p>
			<div class="edit-actions">
				<button type="button" class="primary-btn small" on:click={() => accept(waiting)}>Replace</button>
				<button type="button" class="secondary-btn small" on:click={() => (waiting = null)}>Keep</button>
			</div>
		</div>
	{:else if imported}
		<span class="import-note" role="status">Imported {imported.name}.</span>
	{/if}
	{#if problem}
		<p class="import-problem" role="alert"><Icon name="warning" size={18} /> {problem}</p>
	{/if}
</div>
