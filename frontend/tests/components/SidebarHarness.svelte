<script>
	import Hamburger from '../../src/lib/design/Hamburger.svelte';
	import Sidebar from '../../src/lib/design/Sidebar.svelte';

	export let disabled = false;
	let sidebar;
	let open = false;
	let hamburger = null;
</script>

<button type="button">Before</button>
<Hamburger bind:button={hamburger} {open} controls="side" on:click={() => sidebar.toggle()} />
<Sidebar bind:this={sidebar} bind:open id="side" label="Panel" trigger={hamburger} {disabled}>
	<button type="button">One</button>
	<input aria-label="Name" />
	<button type="button">Two</button>
</Sidebar>
<button type="button">Outside</button>
