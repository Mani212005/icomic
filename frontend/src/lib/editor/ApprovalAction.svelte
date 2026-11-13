<script>
	import Icon from '../design/Icon.svelte';
	import { APPROVAL } from '../api/types.js';
	// One card's approval control. `state` is an APPROVAL value: pending offers Approve, stale says the
	// card was edited since and offers to approve it again, approved just says so.
	export let state = APPROVAL.PENDING;
	export let onApprove = () => {};
	export let label = 'Approve';
	export let staleLabel = 'Approve';
	// Disables the buttons; `title` says why.
	export let disabled = false;
	export let title = undefined;
</script>

{#if state === APPROVAL.STALE}
	<div class="stale-action-box">
		<span class="stale-warning-text"><Icon name="warning" size={18} /> Edited</span>
		<button class="reapprove-btn" {disabled} on:click={onApprove}>{staleLabel}</button>
	</div>
{:else if state === APPROVAL.APPROVED}
	<span class="status-approved"><Icon name="check" size={18} /> Approved</span>
{:else}
	<button class="approve-btn" {disabled} {title} on:click={onApprove}>{label}</button>
{/if}
