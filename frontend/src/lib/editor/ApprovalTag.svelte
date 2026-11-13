<script>
	import Icon from '../design/Icon.svelte';
	import { APPROVAL } from '../api/types.js';
	// The stage header's approval summary: `state` is an APPROVAL value.
	export let state = APPROVAL.PENDING;
</script>

{#if state === APPROVAL.STALE}
	<span class="approval-tag stale"><Icon name="warning" size={18} /> Stale</span>
{:else}
	<span class="approval-tag" class:approved={state === APPROVAL.APPROVED}>
		{#if state === APPROVAL.APPROVED}<Icon name="check" size={14} />{/if}{state === APPROVAL.APPROVED ? 'Approved' : 'Pending'}
	</span>
{/if}
