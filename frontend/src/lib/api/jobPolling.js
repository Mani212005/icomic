
import { JOB_STATUS } from './types.js';

export class JobPollingError extends Error {
	constructor(message, job = null, code = 'JOB_ERROR', options = {}) {
		super(message);
		this.name = 'JobPollingError';
		this.job = job;
		this.code = code;
		this.status = options.status;
		this.retryable = options.retryable === true;
		this.retry_after_seconds = options.retry_after_seconds;
	}
}

// A generation request returns its receipt at once, so polling spans the whole provider call.
export const GENERATION_WAIT_MS = 20 * 60 * 1000;
// Consecutive failed polls (a dropped connection, not an answer about the job) before giving up.
const MAX_POLL_FAILURES = 5;

// A poll that says nothing about the job: the connection dropped, the API is busy or briefly down.
function transientPollError(err) {
	if (!err?.status) return err instanceof TypeError;
	return err.status === 429 || err.status >= 500;
}

export async function pollJob(apiClient, jobId, options = {}) {
	const {
		intervalMs = 1000,
		// A job that runs long is polled less often, which keeps the per-user request budget for the page.
		maxIntervalMs = intervalMs,
		timeoutMs = GENERATION_WAIT_MS,
		onProgress = null,
		signal = null
	} = options;

	const startTime = Date.now();
	let failedPolls = 0;
	let delayMs = intervalMs;

	while (true) {
		if (signal && signal.aborted) {
			throw new JobPollingError('Job polling aborted by client', null, 'ABORTED');
		}

		if (Date.now() - startTime > timeoutMs) {
			throw new JobPollingError(`Job ${jobId} timed out after ${timeoutMs}ms`, null, 'TIMEOUT');
		}

		let job;
		try {
			job = await apiClient.getJob(jobId);
			failedPolls = 0;
		} catch (err) {
			// The job is not lost when a poll is: keep asking until the connection returns.
			if (!transientPollError(err)) throw err;
			failedPolls += 1;
			if (failedPolls >= MAX_POLL_FAILURES) {
				throw new JobPollingError(
					`Lost contact with the server. Job ${jobId} may still be running.`,
					null,
					'POLL_FAILED',
					{ retryable: true }
				);
			}
			const retryAfterMs = Number(err.retry_after_seconds) * 1000;
			await new Promise((resolve) => setTimeout(resolve, Math.max(delayMs, retryAfterMs || 0)));
			delayMs = Math.min(maxIntervalMs, delayMs * 1.5);
			continue;
		}

		if (onProgress) {
			try {
				onProgress(job);
			} catch (e) {
				console.error('Error in onProgress callback:', e);
			}
		}

		if (job.status === JOB_STATUS.DONE) {
			return job;
		}

		if (job.status === JOB_STATUS.FAILED) {
			throw new JobPollingError(
				job.error_message || `Job ${jobId} failed`,
				job,
				job.error_code || 'JOB_FAILED',
				{
					status: job.error_code === 'RATE_LIMITED' ? 429 : undefined,
					retryable: job.retryable,
					retry_after_seconds: job.retry_after_seconds
				}
			);
		}

		if (job.status === JOB_STATUS.UNCERTAIN) {
			// The provider may have billed; the debit stays and a blind retry could charge twice.
			throw new JobPollingError(
				job.error_message || `The outcome of job ${jobId} could not be confirmed`,
				job,
				'OUTCOME_UNKNOWN'
			);
		}

		if (job.status === JOB_STATUS.CANCELLED) {
			throw new JobPollingError(
				`Job ${jobId} was cancelled`,
				job,
				'JOB_CANCELLED'
			);
		}

		await new Promise((resolve, reject) => {
			const timer = setTimeout(resolve, delayMs);
			if (signal) {
				signal.addEventListener(
					'abort',
					() => {
						clearTimeout(timer);
						reject(new JobPollingError('Job polling aborted by client', job, 'ABORTED'));
					},
					{ once: true }
				);
			}
		});
		delayMs = Math.min(maxIntervalMs, delayMs * 1.5);
	}
}
