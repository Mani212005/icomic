<script>
	import Icon from '#lib/design/Icon.svelte';
	import { loginErrorMessage } from '#lib/account/session.js';

	export let client;
	export let note = '';
	export let done = () => {};

	let signingUp = false;
	let email = '';
	let password = '';
	let busy = false;
	let error = '';

	async function submit() {
		busy = true;
		error = '';
		try {
			const result = signingUp ? await client.signUp(email, password) : await client.signIn(email, password);
			password = '';
			done(result);
		} catch (err) {
			error = loginErrorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<section class="auth-card" aria-labelledby="auth-title">
	<div class="auth-tabs" role="group" aria-label="Account">
		<button type="button" aria-pressed={!signingUp} on:click={() => (signingUp = false)}>Sign in</button>
		<button type="button" aria-pressed={signingUp} on:click={() => (signingUp = true)}>Sign up</button>
	</div>

	<h1 id="auth-title">Account</h1>
	{#if note}<p class="auth-note">{note}</p>{/if}
	{#if error}
		<div class="modal-error" role="alert"><Icon name="warning" size={18} /> {error}</div>
	{/if}

	<form class="modal-form" on:submit|preventDefault={submit} aria-busy={busy}>
		<div class="form-group">
			<label for="auth-email">Email</label>
			<input id="auth-email" type="email" autocomplete="email" maxlength="254" required bind:value={email} />
		</div>
		<div class="form-group">
			<label for="auth-password">Password</label>
			<input
				id="auth-password"
				type="password"
				autocomplete={signingUp ? 'new-password' : 'current-password'}
				minlength={signingUp ? 10 : undefined}
				maxlength="256"
				required
				bind:value={password}
			/>
			{#if signingUp}<span class="auth-hint">At least 10 characters.</span>{/if}
		</div>
		<button type="submit" class="primary-btn" disabled={busy}>
			Continue
		</button>
	</form>
</section>
