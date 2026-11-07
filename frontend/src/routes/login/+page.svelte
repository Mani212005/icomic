<script>
	import '#lib/design/theme.css';
	import { AppHeader, AppFooter } from '#lib/design/shell.js';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import LoginForm from '#lib/account/LoginForm.svelte';
	import { sharedClient } from '#lib/account/session.js';

	const client = sharedClient();
	let note = '';
	let ready = false;

	onMount(async () => {
		const limited = new URLSearchParams(window.location.search).has('limit');
		try {
			const account = await client.getAccount();
			if (account?.email) return goto('/', { replaceState: true });
			if (limited) note = 'Guests keep one project. Sign in for more.';
			else if (account?.project_count) note = 'Your project stays with you.';
		} catch {
			// The form still works; the server reports real problems on submit.
		}
		ready = true;
	});
</script>

<div class="app-layout">
	<AppHeader>
		<svelte:fragment slot="account"><a class="account-link" href="/">Back</a></svelte:fragment>
	</AppHeader>
	<main class="auth-main">
		{#if ready}
			<LoginForm {client} {note} done={() => goto('/')} />
		{/if}
	</main>
	<AppFooter />
</div>
