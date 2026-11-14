<script>
	import { SAVE } from './autosave.js';
	import { formatApiError } from '#lib/api/types.js';

	export let state = SAVE.IDLE;
	export let error = null;
	export let onRetry = () => {};

	$: reason = error ? formatApiError(error).message : '';
</script>

<span class="save-status" class:save-failed={state === SAVE.ERROR} role="status" aria-live="polite" data-state={state}>
	{#if state === SAVE.DIRTY}
		Unsaved changes
	{:else if state === SAVE.SAVING}
		Saving…
	{:else if state === SAVE.SAVED}
		Saved
	{:else if state === SAVE.ERROR}
		Not saved: {reason}
		<button type="button" class="secondary-btn small" on:click={onRetry}>Retry</button>
	{/if}
</span>
