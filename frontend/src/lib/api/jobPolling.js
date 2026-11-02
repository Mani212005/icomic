/**
 * Job polling utility for Icomic generation jobs.
 *
 * Implements polling against GET /api/v1/generation-jobs/:id until
 * job enters a terminal state (done, failed, cancelled) or times out.
 * Supports cancellation via AbortSignal and progress notification callbacks.
 */

import { JOB_STATUS } from './types.js';

export class JobPollingError extends Error {
	constructor(message, job = null, code = 'JOB_ERROR') {
		super(message);
		this.name = 'JobPollingError';
		this.job = job;
		this.code = code;
	}
}

/**
 * Poll a generation job until completion, failure, timeout, or cancellation.
 *
 * @param {import('./client.js').ApiClient|import('./mockClient.js').MockApiClient} apiClient
 * @param {string} jobId
 * @param {Object} [options={}]
 * @param {number} [options.intervalMs=1000] Polling interval in milliseconds
 * @param {number} [options.timeoutMs=60000] Maximum poll duration before timeout
 * @param {(job: any) => void} [options.onProgress] Callback fired on each poll
 * @param {AbortSignal} [options.signal] AbortSignal for client-side cancellation
 * @returns {Promise<any>} Resolves with the completed job object
 */
export async function pollJob(apiClient, jobId, options = {}) {
	const {
		intervalMs = 1000,
		timeoutMs = 60000,
		onProgress = null,
		signal = null
	} = options;

	const startTime = Date.now();

	while (true) {
		if (signal && signal.aborted) {
			throw new JobPollingError('Job polling aborted by client', null, 'ABORTED');
		}

		if (Date.now() - startTime > timeoutMs) {
			throw new JobPollingError(`Job ${jobId} timed out after ${timeoutMs}ms`, null, 'TIMEOUT');
		}

		const job = await apiClient.getJob(jobId);

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
				'JOB_FAILED'
			);
		}

		if (job.status === JOB_STATUS.CANCELLED) {
			throw new JobPollingError(
				`Job ${jobId} was cancelled`,
				job,
				'JOB_CANCELLED'
			);
		}

		// Wait interval before next poll
		await new Promise((resolve, reject) => {
			const timer = setTimeout(resolve, intervalMs);
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
	}
}
