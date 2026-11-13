// The job a project is waiting on, remembered by ID so a reload, a closed tab or a dropped
// connection resumes polling it instead of submitting (and paying for) another generation.
const PREFIX = 'icomic.activeJob.';
const MAX_AGE_MS = 24 * 60 * 60 * 1000;

function store(storage) {
	try {
		return storage ?? globalThis.localStorage ?? null;
	} catch {
		return null;
	}
}

export function rememberActiveJob(projectId, record, storage) {
	try {
		store(storage)?.setItem(PREFIX + projectId, JSON.stringify({ ...record, savedAt: Date.now() }));
	} catch {
		// Storage can be blocked; the page then only follows the job while it stays open.
	}
}

export function recallActiveJob(projectId, storage) {
	try {
		const raw = store(storage)?.getItem(PREFIX + projectId);
		if (!raw) return null;
		const record = JSON.parse(raw);
		if (!record?.jobId || Date.now() - Number(record.savedAt) > MAX_AGE_MS) {
			forgetActiveJob(projectId, storage);
			return null;
		}
		return record;
	} catch {
		return null;
	}
}

export function forgetActiveJob(projectId, storage) {
	try {
		store(storage)?.removeItem(PREFIX + projectId);
	} catch {
		// Nothing to forget.
	}
}
