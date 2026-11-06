import { writable } from 'svelte/store';
import { previewOverride } from './geometry.js';

export class LetteringSession {
	constructor(api, pageId) {
		this.api = api;
		this.pageId = pageId;
		this.value = { lettering: null, busy: false, error: null, selected: null, undo: [], redo: [] };
		const store = writable(this.value);
		this.subscribe = store.subscribe;
		this.publish = (patch) => { this.value = { ...this.value, ...patch }; store.set(this.value); };
		this.loading = null;
		this.pendingRequest = Promise.resolve();
	}

	load(force = false) {
		if (this.loading && !force) return this.loading;
		this.publish({ busy: true, error: null });
		this.loading = this.api.getLettering(this.pageId).then((response) => {
			const ids = new Set(response.lettering?.input.panels.flatMap((panel) => panel.utterances.map((u) => u.utterance_id)) || []);
			this.publish({ lettering: response.lettering || null, busy: false,
				selected: ids.has(this.value.selected) ? this.value.selected : null,
				undo: this.value.undo.filter((entry) => ids.has(entry.id)), redo: this.value.redo.filter((entry) => ids.has(entry.id)) });
		}).catch(() => {
			this.publish({ busy: false, error: 'Load failed.' });
			this.loading = null;
		});
		return this.loading;
	}

	select(id) { this.publish({ selected: id }); }

	place(id, override, history = 'add') {
		return this.pendingRequest = this.persistPlacement(id, override, history);
	}

	async persistPlacement(id, override, history) {
		if (this.value.busy || !this.value.lettering) return false;
		const before = structuredClone(this.value.lettering);
		const previous = before.overrides[id] || {};
		if (JSON.stringify(previous) === JSON.stringify(override)) return true;
		this.publish({ lettering: previewOverride(before, id, override), busy: true, error: null });
		try {
			const response = await this.api.patchLetteringOverride(this.pageId, id, override);
			if (!response.lettering) throw new Error('Missing layout');
			const entry = { id, before: previous, after: override };
			const undo = history === 'undo' ? this.value.undo.slice(0, -1) : [...this.value.undo, history === 'redo' ? this.value.redo.at(-1) : entry];
			const redo = history === 'undo' ? [...this.value.redo, this.value.undo.at(-1)] : history === 'redo' ? this.value.redo.slice(0, -1) : [];
			this.publish({ lettering: response.lettering, busy: false, undo, redo });
			return true;
		} catch {
			this.publish({ lettering: before, error: 'Save failed.' });
			// A rejected compare-and-save may mean another editor changed this page.
			// Refresh before accepting another edit, keeping the rejection visible.
			try {
				const response = await this.api.getLettering(this.pageId);
				this.publish({ lettering: response.lettering || null, undo: [], redo: [] });
			} catch { /* Keep the last confirmed layer if the server is unavailable. */ }
			this.publish({ busy: false });
			return false;
		}
	}

	undo() { const entry = this.value.undo.at(-1); return entry && this.place(entry.id, entry.before, 'undo'); }
	redo() { const entry = this.value.redo.at(-1); return entry && this.place(entry.id, entry.after, 'redo'); }
	reset(id) { return this.place(id, {}); }

	text(id, text) {
		return this.pendingRequest = this.pendingRequest.then(() => this.persistText(id, text));
	}

	async persistText(id, text) {
		if (this.value.busy || !this.value.lettering) return false;
		this.publish({ busy: true, error: null });
		try {
			const response = await this.api.patchLetteringText(this.pageId, id, text);
			if (!response.lettering) throw new Error('Missing layout');
			// Text changes can remove utterances; placement history remains useful
			// for surviving IDs but cannot resurrect deleted source text.
			const ids = new Set(response.lettering.input.panels.flatMap((p) => p.utterances.map((u) => u.utterance_id)));
			this.publish({ lettering: response.lettering, busy: false, undo: this.value.undo.filter((e) => ids.has(e.id)), redo: [] });
			return true;
		} catch {
			this.publish({ error: 'Save failed.' });
			try {
				const response = await this.api.getLettering(this.pageId);
				this.publish({ lettering: response.lettering || null, undo: [], redo: [] });
			} catch { /* Retain the confirmed source while offline. */ }
			this.publish({ busy: false });
			return false;
		}
	}
}
