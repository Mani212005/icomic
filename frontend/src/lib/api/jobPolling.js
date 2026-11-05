
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
				job.error_code || 'JOB_FAILED',
				{
					status: job.error_code === 'RATE_LIMITED' ? 429 : undefined,
					retryable: job.retryable,
					retry_after_seconds: job.retry_after_seconds
				}
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
