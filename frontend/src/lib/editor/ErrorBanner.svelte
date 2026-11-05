<script>
	import Icon from '#lib/design/Icon.svelte';
	export let error = null;
	export let onDismiss = () => {};
</script>

	{#if error}
		<div class="error-banner error-{error.type}" role="alert">
			<div class="error-header">
				<span class="error-icon"><Icon name="warning" size={18} /></span>
				<strong class="error-title">{error.title}</strong>
				<button class="dismiss-btn" on:click={onDismiss} aria-label="Dismiss"><Icon name="close" size={18} /></button>
			</div>
			<p class="error-msg">{error.message}</p>
			{#if error.action}
				<div class="error-action">
					<span class="action-tag">Next:</span> {error.action}
				</div>
			{/if}
		</div>
	{/if}

