import { pollJob, JobPollingError } from '../api/jobPolling.js';
import { newIdempotencyKey } from '../api/idempotency.js';
import { rememberActiveJob, recallActiveJob, forgetActiveJob } from '../api/activeJobs.js';

export const JOB_KIND = { SCRIPT: 'script', CHARACTER: 'character', PAGE: 'page', PALETTE: 'palette' };

const RESUME_LABEL = 'Keep waiting';

// What the user is told about a cancel. Cancel is pending-only: it really stops a job that has not
// reached the model (nothing is charged), and it never claims to stop one that has. Terminal states
// are never reversed, so a job that is settling finishes with its result and its cost.
export const CANCEL_MESSAGE = {
	cancelled: 'Cancelled, nothing was charged.',
	settling: 'Already with the model, finishing. You will be charged.',
	settled: 'This was already with the model when you cancelled, so it finished and you were charged.',
	finished: 'This had already finished, so there was nothing to cancel.'
};

function progressText(job) {
	if (job.status === 'pending') return 'Queued';
	if (job.status === 'running') return 'Generating';
	return `Status: ${job.status}`;
}

// Runs one paid generation from the click to its finished entity, for the editor.
//
// The request returns a receipt as soon as the job and its charge are committed, so the job is
// followed by ID from then on. The ID is remembered per project: a reload, a closed tab or a dropped
// connection resumes the same job and never submits (or pays for) another. The job keeps running on
// the server whatever happens to this page, so a slow or lost connection pauses the wait and never
// releases the controls; only a settled job does.
//
// Cancel asks the server, which answers honestly: `cancelled` (the job had not reached the model, so
// nothing was charged) releases the controls; `settling` (already with the model) keeps them locked
// until the job reaches its own terminal state, with its result and cost.
export class GenerationRunner {
	constructor({ api, settle, onChange, onError, storage, intervalMs = 500, maxIntervalMs = 3000 }) {
		this.api = api;
		this.settle = settle; // (job) => Promise: show what the finished job produced
		this.onChange = onChange; // ({ active, label, status, stalled }) => void
		this.onError = onError; // (error) => void
		this.storage = storage;
		this.intervalMs = intervalMs;
		this.maxIntervalMs = maxIntervalMs;
		this.projectId = null;
		this.job = null;
		this.abort = null;
		this.view = {
			active: false,
			label: '',
			status: '',
			stalled: '',
			resumeLabel: RESUME_LABEL,
			canCancel: false,
			cancelling: false,
			settling: false,
			notice: ''
		};
	}

	update(changes) {
		this.view = { ...this.view, ...changes };
		this.onChange(this.view);
	}

	get active() {
		return this.view.active;
	}

	release(changes = {}) {
		if (this.projectId) forgetActiveJob(this.projectId, this.storage);
		this.job = null;
		this.abort = null;
		this.update({ active: false, stalled: '', canCancel: false, cancelling: false, settling: false, ...changes });
	}

	clearNotice() {
		if (this.view.notice) this.update({ notice: '' });
	}

	// Cancel is offered only while a job exists on the server to cancel and only until the server
	// has said the job is settling.
	async cancel() {
		const job = this.job;
		if (!job || !this.view.canCancel || this.view.cancelling) return;
		this.update({ cancelling: true });
		let answer;
		try {
			answer = await this.api.cancelJob(job.jobId);
		} catch (err) {
			// Nothing is known to have changed: the job keeps running and may still be cancelled.
			this.update({ cancelling: false });
			this.onError(err);
			return;
		}
		if (this.job !== job) return;
		const outcome = answer?.outcome ?? answer?.job?.status;
		if (outcome === 'cancelled') {
			this.abort?.abort();
			this.release({ notice: CANCEL_MESSAGE.cancelled });
			await this.settle({ kind: 'refresh' }).catch(() => {});
		} else if (outcome === 'settling') {
			job.settling = true;
			this.update({ cancelling: false, canCancel: false, settling: true, status: CANCEL_MESSAGE.settling });
		} else {
			// Already finished: the poll in flight reports its final state in a moment.
			this.update({ cancelling: false, canCancel: false, notice: CANCEL_MESSAGE.finished });
		}
	}

	// Switching projects drops the old wait (it stays remembered under its own project) and resumes
	// whatever the new project was waiting on.
	async setProject(projectId) {
		this.abort?.abort();
		this.projectId = projectId;
		this.job = null;
		this.abort = null;
		this.update({ active: false, stalled: '', canCancel: false, cancelling: false, settling: false, notice: '' });
		return this.resume();
	}

	async resume() {
		if (!this.projectId || this.job) return;
		const record = recallActiveJob(this.projectId, this.storage);
		if (!record) return;
		this.job = record;
		await this.follow();
	}

	// `start(idempotencyKey)` submits the request and resolves with its receipt.
	async run({ kind, label, status, entityId = '', start }) {
		if (this.view.active) return;
		const projectId = this.projectId;
		// Nothing can be cancelled until the receipt names a job.
		this.update({ active: true, label, status, stalled: '', canCancel: false, cancelling: false, settling: false, notice: '' });
		let receipt;
		try {
			receipt = await start(newIdempotencyKey());
		} catch (err) {
			this.release();
			this.onError(err);
			return;
		}
		const jobId = receipt?.job?.id || receipt?.job_id;
		if (!jobId) {
			await this.finish({ kind, entityId });
			return;
		}
		this.job = { jobId, kind, label, entityId };
		rememberActiveJob(projectId, this.job, this.storage);
		await this.follow();
	}

	async follow() {
		const job = this.job;
		if (!job) return;
		const abort = new AbortController();
		this.abort = abort;
		this.update({
			active: true,
			label: job.label,
			status: job.settling ? CANCEL_MESSAGE.settling : 'Working',
			stalled: '',
			canCancel: !job.settling,
			settling: Boolean(job.settling)
		});
		try {
			await pollJob(this.api, job.jobId, {
				intervalMs: this.intervalMs,
				maxIntervalMs: this.maxIntervalMs,
				signal: abort.signal,
				onProgress: (progress) =>
					this.update({ status: this.view.settling ? CANCEL_MESSAGE.settling : progressText(progress) })
			});
		} catch (err) {
			// A cancel that the server confirmed already released the controls.
			if (err instanceof JobPollingError && err.code === 'ABORTED') return;
			if (this.job !== job) return;
			if (err instanceof JobPollingError && err.code === 'JOB_CANCELLED') {
				// Cancelled elsewhere (another tab, or before a reload). Only a job that had not reached
				// the model can be cancelled, so nothing was charged.
				this.release({ notice: CANCEL_MESSAGE.cancelled });
				await this.settle({ kind: 'refresh' }).catch(() => {});
				return;
			}
			if (err instanceof JobPollingError && (err.code === 'TIMEOUT' || err.code === 'POLL_FAILED')) {
				this.update({
					stalled:
						err.code === 'TIMEOUT'
							? 'Still working. Large pages can take several minutes.'
							: 'Lost contact with the server. The job is still saved there.'
				});
				return;
			}
			this.release();
			// A job the server no longer knows (a stale remembered ID) is not an error to show.
			if (err?.status !== 404) this.onError(err);
			await this.settle({ kind: 'refresh' }).catch(() => {});
			return;
		}
		await this.finish(job);
	}

	async finish(job) {
		const settled = Boolean(job?.settling);
		try {
			await this.settle(job);
		} catch (err) {
			this.onError(err);
		} finally {
			this.release(settled ? { notice: CANCEL_MESSAGE.settled } : {});
		}
	}
}
