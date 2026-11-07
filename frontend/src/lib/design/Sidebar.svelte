<script>
	import { onMount, tick } from 'svelte';
	import { SOURCE, EDGE_OPEN_DELAY, LEAVE_CLOSE_DELAY, isTouch, isField, isActivator, isEscape, isTab, focusFirst, trapTab, watchPhone, watchOutside } from './sidebar.js';

	// Reusable slide-out panel. The parent owns the hamburger (see Hamburger.svelte)
	// and passes its element as `trigger` so focus can return to it and clicks on it
	// are not treated as outside clicks. Slot content is the panel body.
	export let open = false;
	export let id;
	export let label;
	export let trigger = null;
	export let disabled = false;

	let panel;
	let isPhone = false;
	let pointerInside = false;
	let closeWhenFieldBlurs = false;
	let openTimer;
	let closeTimer;

	export async function show(source = SOURCE.API) {
		if (open || disabled) return;
		open = true;
		// Hover and edge opens must not steal focus from a field the user is typing in.
		if (source === SOURCE.BUTTON) {
			await tick();
			focusFirst(panel);
		}
	}

	export function hide(source = SOURCE.API) {
		clearTimeout(openTimer);
		clearTimeout(closeTimer);
		closeWhenFieldBlurs = false;
		if (!open) return;
		const focusInside = panel?.contains(document.activeElement);
		open = false;
		if (source === SOURCE.ESCAPE || (source !== SOURCE.OUTSIDE && focusInside)) trigger?.focus();
	}

	export function toggle(source = SOURCE.BUTTON) {
		if (open) hide(source);
		else show(source);
	}

	const fieldFocused = () => panel?.contains(document.activeElement) && isField(document.activeElement);

	function edgeEnter(event) {
		if (isTouch(event) || open || disabled) return;
		clearTimeout(openTimer);
		openTimer = setTimeout(() => {
			if (disabled) return;
			// The pointer is on the screen edge, which the open panel covers.
			pointerInside = true;
			show(SOURCE.EDGE);
		}, EDGE_OPEN_DELAY);
	}

	const edgeLeave = () => clearTimeout(openTimer);

	function panelEnter() {
		pointerInside = true;
		closeWhenFieldBlurs = false;
		clearTimeout(closeTimer);
	}

	function panelLeave(event) {
		pointerInside = false;
		if (isTouch(event) || !open) return;
		clearTimeout(closeTimer);
		closeTimer = setTimeout(() => {
			if (pointerInside || !open) return;
			if (fieldFocused()) closeWhenFieldBlurs = true;
			else hide(SOURCE.LEAVE);
		}, LEAVE_CLOSE_DELAY);
	}

	function panelFocusOut() {
		if (!closeWhenFieldBlurs) return;
		setTimeout(() => {
			if (closeWhenFieldBlurs && !pointerInside && !fieldFocused()) hide(SOURCE.LEAVE);
		});
	}

	function panelClick(event) {
		// On a phone the drawer covers the page, so choosing something dismisses it.
		if (isPhone && isActivator(event.target)) hide(SOURCE.SELECT);
	}

	function outsidePointer(event) {
		if (!open || panel?.contains(event.target) || trigger?.contains(event.target)) return;
		hide(SOURCE.OUTSIDE);
	}

	function keydown(event) {
		if (!open) return;
		if (isEscape(event)) {
			event.preventDefault();
			hide(SOURCE.ESCAPE);
		} else if (isTab(event) && isPhone) {
			trapTab(event, panel, trigger);
		}
	}

	$: if (disabled && open) hide(SOURCE.DISABLED);

	onMount(() => {
		const stopPhone = watchPhone((matches) => (isPhone = matches));
		const stopOutside = watchOutside(outsidePointer);
		return () => {
			stopPhone();
			stopOutside();
			clearTimeout(openTimer);
			clearTimeout(closeTimer);
		};
	});
</script>

<svelte:window on:keydown={keydown} />

<div class="sidebar-edge" data-testid="sidebar-edge" aria-hidden="true" class:off={open || disabled} on:pointerenter={edgeEnter} on:pointerleave={edgeLeave}></div>
<div class="sidebar-scrim" class:open aria-hidden="true"></div>
<div
	{id}
	class="sidebar"
	class:open
	bind:this={panel}
	role={isPhone ? 'dialog' : 'region'}
	aria-modal={isPhone && open ? 'true' : undefined}
	aria-label={label}
	tabindex="-1"
	inert={!open}
	on:pointerenter={panelEnter}
	on:pointerleave={panelLeave}
	on:focusout={panelFocusOut}
	on:click={panelClick}
>
	<slot />
</div>

<style>
	.sidebar-edge {
		position: fixed; z-index: 24; left: 0; bottom: 0; top: var(--header-h, 72px); width: 14px;
	}
	.sidebar-edge.off { pointer-events: none; }
	.sidebar-scrim { display: none; }
	.sidebar {
		position: fixed; z-index: 30; left: 0; bottom: 0; top: var(--header-h, 72px);
		width: min(272px, 86vw); overflow-y: auto; overscroll-behavior: contain;
		padding: 18px 14px 24px; outline: none;
		border-right: 1px solid var(--ink); border-radius: 0 12px 6px 0;
		background: linear-gradient(#FCFAF7e6, #FCFAF7f2), var(--paper-texture) center / cover;
		box-shadow: 4px 0 0 var(--accent), 12px 0 36px #4B454226;
		transform: translate3d(-102%, 0, 0); opacity: 0; visibility: hidden;
		transition: transform 240ms cubic-bezier(.2, .8, .2, 1), opacity 200ms ease, visibility 0s linear 240ms;
	}
	.sidebar.open {
		transform: none; opacity: 1; visibility: visible;
		transition: transform 240ms cubic-bezier(.2, .8, .2, 1), opacity 200ms ease, visibility 0s;
	}
	@media (any-hover: none) {
		.sidebar-edge { display: none; }
	}
	@media (max-width: 700px) {
		.sidebar-edge { display: none; }
		.sidebar { width: min(320px, 88vw); }
		.sidebar-scrim {
			display: block; position: fixed; z-index: 25; left: 0; right: 0; bottom: 0; top: var(--header-h, 60px);
			background: #4B454266; opacity: 0; visibility: hidden;
			transition: opacity 200ms ease, visibility 0s linear 200ms;
		}
		.sidebar-scrim.open { opacity: 1; visibility: visible; transition: opacity 200ms ease, visibility 0s; }
	}
	@media (prefers-reduced-motion: reduce) {
		.sidebar, .sidebar.open, .sidebar-scrim, .sidebar-scrim.open { transition: none; }
	}
</style>
