// Debounced autosave for one editable value (a form's fields as a plain object).
//
// Saves are single-flight: at most one request is outstanding, and edits made while it runs are saved
// afterwards, in order, as one request with the latest value. A slow earlier save therefore can never land
// after, and overwrite, a newer one, and its answer can never mark newer edits as saved. `save(value, saved)`
// gets the value to store and the last value known stored, so it can send only what changed.

export const SAVE = { IDLE: 'idle', DIRTY: 'dirty', SAVING: 'saving', SAVED: 'saved', ERROR: 'error' };

const sameValue = (a, b) => JSON.stringify(a) === JSON.stringify(b);

export class Autosaver {
	constructor({ save, saved, delay = 1000, onChange = () => {} }) {
		this.saveValue = save;
		this.saved = saved;
		this.latest = saved;
		this.delay = delay;
		this.onChange = onChange;
		this.state = SAVE.IDLE;
		this.error = null;
		this.version = 0;
		this.dirty = false;
		this.timer = null;
		this.running = null;
		this.disposed = false;
	}

	setState(state, error = null) {
		if (state === this.state && error === this.error) return;
		this.state = state;
		this.error = error;
		if (!this.disposed) this.onChange(state, error);
	}

	// The form changed. Saves after `delay` of quiet; a value equal to what is stored needs no save.
	update(value) {
		if (this.disposed) return;
		this.latest = value;
		this.version += 1;
		clearTimeout(this.timer);
		this.timer = null;
		if (sameValue(value, this.saved)) {
			this.dirty = false;
			if (!this.running) this.setState(this.state === SAVE.IDLE ? SAVE.IDLE : SAVE.SAVED);
			return;
		}
		this.dirty = true;
		if (!this.running) this.setState(SAVE.DIRTY);
		this.timer = setTimeout(() => this.flush().catch(() => {}), this.delay);
	}

	// Saves now. Resolves once everything edited so far is stored; rejects with the save's error, leaving the
	// edits pending so retry() (or the next edit) tries again.
	flush() {
		clearTimeout(this.timer);
		this.timer = null;
		if (!this.running) this.running = this.drain().finally(() => (this.running = null));
		return this.running;
	}

	retry() {
		return this.flush();
	}

	async drain() {
		while (this.dirty) {
			const value = this.latest;
			const version = this.version;
			this.setState(SAVE.SAVING);
			try {
				await this.saveValue(value, this.saved);
			} catch (error) {
				this.setState(SAVE.ERROR, error);
				throw error;
			}
			this.saved = value;
			// Edits made while this request ran keep the item dirty and go out next.
			if (this.version === version) this.dirty = false;
		}
		this.setState(SAVE.SAVED);
	}

	// The form is going away: send what is pending, and stop reporting.
	async dispose() {
		try {
			if (this.dirty) await this.flush();
		} catch {
			// Nothing is left to show the error on.
		} finally {
			clearTimeout(this.timer);
			this.disposed = true;
		}
	}
}
