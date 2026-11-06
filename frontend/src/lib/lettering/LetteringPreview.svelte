<script>
	import { onMount, onDestroy, tick } from 'svelte';
	import Icon from '#lib/design/Icon.svelte';
	import LetteringLayer from './LetteringLayer.svelte';
	import { utterances, movedOverride, nudge, previewOverride, tipOf, panelFor, pagePoint, centerOf } from './geometry.js';

	export let session;
	export let imageUrl;
	export let imageAlt;
	export let onImageError = () => {};
	let state = session.value;
	const unsubscribe = session.subscribe((value) => state = value);
	let frame;
	let editor;
	let editing = null;
	let editText = '';
	let gesture = null;
	let pending = null;
	let timer;
	let textTimer;
	$: layer = pending && state.lettering ? previewOverride(state.lettering, pending.id, pending.override) : state.lettering;
	$: bubbles = layer?.layout.bubbles || [];
	$: entries = utterances(state.lettering);
	$: selected = bubbles.find((b) => b.utterance_id === state.selected);
	$: selectedEntry = entries.find((u) => u.utterance_id === state.selected);
	$: tailPoint = selected && (layer.overrides[state.selected]?.tail_anchor ? pagePoint(layer.overrides[state.selected].tail_anchor, panelFor(layer, state.selected)) : tipOf(selected) || centerOf(selected));
	$: flags = layer?.layout.issues || [];
	$: editingBounds = selected?.shape.bounds || { x: 0.05, y: 0.05, width: 0.9 };

	onMount(() => session.load());
	onDestroy(() => {
		clearTimeout(timer);
		clearTimeout(textTimer);
		if (pending) session.place(pending.id, pending.override);
		if (editing && editText !== entries.find((u) => u.utterance_id === editing)?.text) session.text(editing, editText);
		unsubscribe();
	});

	function flush() {
		clearTimeout(timer);
		if (!pending) return;
		const edit = pending;
		pending = null;
		return session.place(edit.id, edit.override);
	}
	function schedule(id, override) {
		pending = { id, override };
		clearTimeout(timer);
		timer = setTimeout(flush, 220);
	}
	function select(id) {
		if (state.busy || gesture || editing) return;
		if (pending?.id !== id) flush();
		session.select(id);
	}
	function start(event, bubble, tail = false) {
		if (event.button !== 0 || state.busy || editing) return;
		if (pending) { flush(); return; }
		select(bubble.utterance_id);
		event.currentTarget.focus({ preventScroll: true });
		event.currentTarget.setPointerCapture(event.pointerId);
		gesture = { id: bubble.utterance_id, x: event.clientX, y: event.clientY, tail, pointerId: event.pointerId, rect: frame.getBoundingClientRect(), original: state.lettering };
	}
	function move(event) {
		if (!gesture || event.pointerId !== gesture.pointerId) return;
		const delta = { x: (event.clientX - gesture.x) / gesture.rect.width, y: (event.clientY - gesture.y) / gesture.rect.height };
		if (!delta.x && !delta.y) return;
		pending = { id: gesture.id, override: movedOverride(gesture.original, gesture.id, delta, gesture.tail) };
	}
	function end(event) {
		if (!gesture || event.pointerId !== gesture.pointerId) return;
		gesture = null;
		flush();
	}
	function cancel() { gesture = null; pending = null; clearTimeout(timer); }
	function key(event, bubble, tail = false) {
		if (event.key === 'Escape') { cancel(); session.select(null); event.currentTarget.blur(); return; }
		if (state.busy) return;
		if (event.key === 'Enter' && !tail) { event.preventDefault(); beginEdit(bubble.utterance_id); return; }
		const delta = nudge(event);
		if (!delta) return;
		event.preventDefault();
		select(bubble.utterance_id);
		schedule(bubble.utterance_id, movedOverride(layer, bubble.utterance_id, delta, tail));
	}
	async function beginEdit(id) {
		await flush();
		if (state.busy) return;
		session.select(id);
		editing = id;
		editText = entries.find((u) => u.utterance_id === id)?.text || '';
		await tick();
		editor?.focus();
	}
	function scheduleText() { clearTimeout(textTimer); textTimer = setTimeout(saveText, 350); }
	async function saveText() {
		if (!editing) return;
		if (state.busy) { scheduleText(); return; }
		const id = editing, text = editText;
		if (entries.find((u) => u.utterance_id === id)?.text === text) return;
		const ok = await session.text(id, text);
		if (!ok) {
			clearTimeout(textTimer);
			editText = entries.find((u) => u.utterance_id === id)?.text || '';
		}
	}
	async function leaveEdit(event) {
		if (event.key !== 'Escape' && !(event.key === 'Enter' && (event.ctrlKey || event.metaKey))) return;
		event.preventDefault();
		clearTimeout(textTimer);
		const id = editing;
		if (editText !== entries.find((u) => u.utterance_id === id)?.text) session.text(id, editText);
		editing = null;
		await tick();
		[...frame.querySelectorAll('[data-body]')].find((element) => element.dataset.body === id)?.focus();
	}
	function scale(event) {
		if (!selected || state.busy) return;
		schedule(state.selected, { ...(layer.overrides[state.selected] || {}), size_scale: Number(event.currentTarget.value) });
	}
	const bodyOf = (event) => bubbles.find((b) => b.utterance_id === event.currentTarget.dataset.body);
	function focusBody(event) { select(event.currentTarget.dataset.body); }
	function startBody(event) { start(event, bodyOf(event)); }
	function startTail(event) { start(event, selected, true); }
	function keyBody(event) { key(event, bodyOf(event)); }
	function keyTail(event) { key(event, selected, true); }
	function editBody(event) { beginEdit(event.currentTarget.dataset.body); }
	function choose(event) { select(event.currentTarget.value || null); }
	function undo() { session.undo(); }
	function redo() { session.redo(); }
	function editSelected() { beginEdit(state.selected); }
	function resetSelected() { session.reset(state.selected); }
	function reload() { session.load(true); }
</script>

<div class="lettering-preview">
	<div class="lettering-frame" bind:this={frame}>
		<img src={imageUrl} alt={imageAlt} on:error={onImageError} draggable="false" />
		{#if layer}
			<LetteringLayer layout={layer.layout} idPrefix={session.pageId} />
			{#each bubbles as bubble (bubble.utterance_id)}
				{@const b = bubble.shape.bounds}
				{@const entry = entries.find((u) => u.utterance_id === bubble.utterance_id)}
				<button type="button" class="bubble-handle" class:selected={state.selected === bubble.utterance_id} data-body={bubble.utterance_id}
					style:left="{b.x * 100}%" style:top="{b.y * 100}%" style:width="{b.width * 100}%" style:height="{b.height * 100}%"
					aria-label={entry?.speaker ? `${entry.speaker}: ${entry.text}` : entry?.text || bubble.utterance_id} aria-pressed={state.selected === bubble.utterance_id}
					on:focus={focusBody} on:pointerdown={startBody} on:pointermove={move} on:pointerup={end} on:pointercancel={cancel} on:lostpointercapture={cancel} on:keydown={keyBody} on:dblclick={editBody}
				></button>
			{/each}
			{#if selected && tailPoint && ['speech', 'thought'].includes(selected.kind)}
				<button type="button" class="tail-handle" style:left="{tailPoint.x * 100}%" style:top="{tailPoint.y * 100}%" aria-label="Tail"
					on:pointerdown={startTail} on:pointermove={move} on:pointerup={end} on:pointercancel={cancel} on:lostpointercapture={cancel} on:keydown={keyTail}><span></span></button>
			{/if}
			{#if editing}
				<textarea class="inline-text" bind:this={editor} bind:value={editText} on:input={scheduleText} on:keydown={leaveEdit} aria-label="Text" name="text" maxlength="8000"
					style:left="{Math.min(0.6, Math.max(0, editingBounds.x)) * 100}%" style:top="{Math.min(0.8, Math.max(0, editingBounds.y)) * 100}%" style:width="{Math.min(1 - Math.min(0.6, Math.max(0, editingBounds.x)), Math.max(0.4, editingBounds.width)) * 100}%"></textarea>
			{/if}
		{/if}
	</div>
	{#if layer}
		<div class="lettering-tools" aria-label="Lettering" role="group">
			<select aria-label="Utterance" name="utterance" value={state.selected || ''} disabled={state.busy || !!editing} on:change={choose}>
				<option value="">Lettering</option>
				{#each entries as entry}<option value={entry.utterance_id}>{entry.text || entry.utterance_id}</option>{/each}
			</select>
			<button type="button" aria-label="Undo" disabled={state.busy || !!pending || !!editing || !state.undo.length} on:click={undo}><Icon name="undo" size={18} /></button>
			<button type="button" aria-label="Redo" disabled={state.busy || !!pending || !!editing || !state.redo.length} on:click={redo}><Icon name="redo" size={18} /></button>
			{#if selectedEntry}
				<input aria-label="Size" name="size" type="range" min="0.875" max="3" step="0.025" value={layer.overrides[state.selected]?.size_scale || 1} disabled={state.busy || !!editing} on:input={scale} />
				<button type="button" aria-label="Edit" disabled={state.busy || !!editing} on:click={editSelected}><Icon name="edit" size={18} /></button>
				<button type="button" aria-label="Reset" disabled={state.busy || !!pending || !!editing || !state.lettering.overrides[state.selected]} on:click={resetSelected}><Icon name="refresh" size={18} /></button>
			{/if}
			{#each flags as flag}
				<span title={flag.flag.replaceAll('_', ' ')}><Icon name="warning" label={flag.flag.replaceAll('_', ' ')} size={18} /></span>
			{/each}
		</div>
	{/if}
	{#if state.error}<div role="alert" class="lettering-error">{state.error}<button type="button" aria-label="Retry" disabled={state.busy} on:click={reload}><Icon name="refresh" size={18} /></button></div>{/if}
</div>

<style>
	@font-face { font-family: 'Comic Neue'; src: url('/fonts/comic-neue-v1.woff') format('woff'); font-display: swap; }
	@font-face { font-family: 'Noto Lettering'; src: url('/fonts/noto-devanagari-v1.woff') format('woff'); font-display: swap; }
	.lettering-preview { width: 100%; min-width: 0; }
	.lettering-frame { position: relative; width: 100%; line-height: 0; isolation: isolate; }
	.lettering-frame img { display: block; width: 100%; height: auto; max-height: none; box-shadow: 2px 4px 10px #4b454220; }
	.bubble-handle { position: absolute; background: transparent; border: 0; border-radius: 45%; padding: 0; cursor: grab; touch-action: none; min-height: 0; }
	.bubble-handle::before { content: ''; position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); width: 100%; height: 100%; min-width: 44px; min-height: 44px; }
	.bubble-handle.selected { outline: 2px solid var(--focus); outline-offset: 3px; }
	.bubble-handle:active { cursor: grabbing; }
	.bubble-handle:focus-visible, .tail-handle:focus-visible { outline: 3px solid var(--focus); outline-offset: 4px; box-shadow: 0 0 0 6px var(--sheet); }
	.tail-handle { position: absolute; transform: translate(-50%, -50%); display: grid; place-items: center; width: 44px; height: 44px; min-height: 44px; padding: 0; background: transparent; border: 0; cursor: crosshair; touch-action: none; }
	.tail-handle span { width: 12px; height: 12px; border-radius: 50%; border: 2px solid var(--sheet); background: var(--focus); box-shadow: 0 0 0 1px var(--focus); }
	.inline-text { position: absolute; resize: vertical; min-height: 75px; padding: 8px; font: 16px/1.35 'Comic Neue', 'Noto Lettering', sans-serif; background: white; color: black; border: 2px solid var(--focus); border-radius: 5px; z-index: 2; }
	.lettering-tools { display: flex; flex-wrap: wrap; gap: 4px; align-items: center; margin-top: 14px; line-height: 1.4; }
	.lettering-tools select { min-width: 0; flex: 1 1 90px; width: 90px; text-overflow: ellipsis; }
	.lettering-tools button, .lettering-error button { width: 44px; height: 44px; padding: 0; display: grid; place-items: center; background: var(--sheet); color: var(--ink); border: 1px solid var(--line); border-radius: 8px; }
	.lettering-tools input { width: 85px; min-width: 65px; flex: 1 1 65px; accent-color: var(--focus); }
	.lettering-tools :global(.ink-icon[role='img']) { color: var(--danger); }
	.lettering-error { display: flex; align-items: center; justify-content: space-between; color: var(--danger); margin-top: 8px; }
	@media (prefers-reduced-motion: reduce) { button, input, textarea { transition: none; animation: none; } }
</style>
