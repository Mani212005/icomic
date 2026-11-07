<script>
	import { createEventDispatcher } from 'svelte';
	import Icon from './Icon.svelte';

	export let stages = [];
	export let active = '';
	export let completed = {};
	export let disabled = false;
	const dispatch = createEventDispatcher();
</script>

<nav class="stepper-nav" aria-label="Stages">
	{#each stages as stage}
		<button
			type="button"
			class="step-item"
			class:active={active === stage.id}
			class:complete={completed[stage.id]}
			aria-current={active === stage.id ? 'step' : undefined}
			{disabled}
			on:click={() => dispatch('select', stage.id)}
		>
			<Icon name={stage.id} size={22} />
			<span class="step-title">{stage.label}</span>
			{#if completed[stage.id]}<span class="completion-mark"><Icon name="check" label="Complete" size={15} /></span>{/if}
		</button>
	{/each}
</nav>
