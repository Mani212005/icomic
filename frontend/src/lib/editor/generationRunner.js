import { pollJob, JobPollingError } from '../api/jobPolling.js';
import { newIdempotencyKey } from '../api/idempotency.js';
import { rememberActiveJob, recallActiveJob, forgetActiveJob } from '../api/activeJobs.js';

export const JOB_KIND = { SCRIPT: 'script', CHARACTER: 'character', PAGE: 'page', PALETTE: 'palette' };

const RESUME_LABEL = 'Keep waiting';

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
		this.view = { active: false, label: '', status: '', stalled: '', resumeLabel: RESUME_LABEL };
	}

	update(changes) {
		this.view = { ...this.view, ...changes };
		this.onChange(this.view);
	}

	get active() {
		return this.view.active;
	}

	release() {
		if (this.projectId) forgetActiveJob(this.projectId, this.storage);
		this.job = null;
		this.update({ active: false, stalled: '' });
	}

	// Switching projects drops the old wait (it stays remembered under its own project) and resumes
	// whatever the new project was waiting on.
	async setProject(projectId) {
		this.projectId = projectId;
		this.job = null;
		this.update({ active: false, stalled: '' });
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
		this.update({ active: true, label, status, stalled: '' });
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
		this.update({ active: true, label: job.label, status: 'Working', stalled: '' });
		try {
			await pollJob(this.api, job.jobId, {
				intervalMs: this.intervalMs,
				maxIntervalMs: this.maxIntervalMs,
				onProgress: (progress) => this.update({ status: progressText(progress) })
			});
		} catch (err) {
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
		try {
			await this.settle(job);
		} catch (err) {
			this.onError(err);
		} finally {
			this.release();
		}
	}
}
