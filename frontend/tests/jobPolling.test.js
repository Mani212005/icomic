import test from 'node:test';
import assert from 'node:assert/strict';
import { pollJob, JobPollingError } from '../src/lib/api/jobPolling.js';
import { JOB_STATUS } from '../src/lib/api/types.js';

test('pollJob resolves when job reaches done state', async () => {
	let callCount = 0;
	const mockClient = {
		async getJob(id) {
			callCount++;
			return {
				id,
				status: callCount < 3 ? JOB_STATUS.RUNNING : JOB_STATUS.DONE,
				actual_cost: 0.01
			};
		}
	};

	const progressReports = [];
	const job = await pollJob(mockClient, 'job-123', {
		intervalMs: 10,
		onProgress: (j) => progressReports.push(j.status)
	});

	assert.equal(job.status, JOB_STATUS.DONE);
	assert.equal(callCount, 3);
	assert.deepEqual(progressReports, [JOB_STATUS.RUNNING, JOB_STATUS.RUNNING, JOB_STATUS.DONE]);
});

test('pollJob rejects with JobPollingError when job fails', async () => {
	const mockClient = {
		async getJob(id) {
			return {
				id,
				status: JOB_STATUS.FAILED,
				error_message: 'Safety filter violation in image generation'
			};
		}
	};

	await assert.rejects(
		async () => pollJob(mockClient, 'job-fail', { intervalMs: 5 }),
		(err) => {
			assert.ok(err instanceof JobPollingError);
			assert.equal(err.code, 'JOB_FAILED');
			assert.match(err.message, /Safety filter violation/);
			return true;
		}
	);
});

test('pollJob preserves structured retry guidance from a failed job', async () => {
	const mockClient = {
		async getJob(id) {
			return {
				id,
				status: JOB_STATUS.FAILED,
				error_code: 'RATE_LIMITED',
				error_message: 'Generation is temporarily rate limited.',
				retryable: true,
				retry_after_seconds: 9
			};
		}
	};

	await assert.rejects(
		async () => pollJob(mockClient, 'job-limited'),
		(err) => {
			assert.ok(err instanceof JobPollingError);
			assert.equal(err.code, 'RATE_LIMITED');
			assert.equal(err.retryable, true);
			assert.equal(err.retry_after_seconds, 9);
			return true;
		}
	);
});

test('pollJob rejects when job is cancelled', async () => {
	const mockClient = {
		async getJob(id) {
			return {
				id,
				status: JOB_STATUS.CANCELLED
			};
		}
	};

	await assert.rejects(
		async () => pollJob(mockClient, 'job-cancel', { intervalMs: 5 }),
		(err) => {
			assert.ok(err instanceof JobPollingError);
			assert.equal(err.code, 'JOB_CANCELLED');
			return true;
		}
	);
});

test('pollJob rejects on timeout', async () => {
	const mockClient = {
		async getJob(id) {
			return { id, status: JOB_STATUS.RUNNING };
		}
	};

	await assert.rejects(
		async () => pollJob(mockClient, 'job-timeout', { intervalMs: 5, timeoutMs: 25 }),
		(err) => {
			assert.ok(err instanceof JobPollingError);
			assert.equal(err.code, 'TIMEOUT');
			return true;
		}
	);
});

test('pollJob cancels immediately when AbortSignal fires', async () => {
	const mockClient = {
		async getJob(id) {
			return { id, status: JOB_STATUS.RUNNING };
		}
	};

	const controller = new AbortController();
	setTimeout(() => controller.abort(), 15);

	await assert.rejects(
		async () => pollJob(mockClient, 'job-abort', { intervalMs: 10, signal: controller.signal }),
		(err) => {
			assert.ok(err instanceof JobPollingError);
			assert.equal(err.code, 'ABORTED');
			return true;
		}
	);
});

test('pollJob treats an uncertain job as final and never reports it as retryable', async () => {
	const mockClient = {
		async getJob(id) {
			return { id, status: JOB_STATUS.UNCERTAIN, error_message: 'The generation outcome could not be confirmed.' };
		}
	};
	await assert.rejects(
		async () => pollJob(mockClient, 'job-uncertain', { intervalMs: 5 }),
		(err) => {
			assert.ok(err instanceof JobPollingError);
			assert.equal(err.code, 'OUTCOME_UNKNOWN');
			assert.equal(err.retryable, false);
			assert.match(err.message, /could not be confirmed/);
			return true;
		}
	);
});

test('pollJob keeps following a job across a dropped connection', async () => {
	let calls = 0;
	const mockClient = {
		async getJob(id) {
			calls += 1;
			if (calls === 2 || calls === 3) throw new TypeError('Failed to fetch');
			return { id, status: calls < 5 ? JOB_STATUS.RUNNING : JOB_STATUS.DONE };
		}
	};
	const job = await pollJob(mockClient, 'job-flaky', { intervalMs: 1 });
	assert.equal(job.status, JOB_STATUS.DONE);
	assert.equal(calls, 5);
});

test('pollJob reports lost contact, not failure, when the connection stays down', async () => {
	const mockClient = {
		async getJob() {
			throw new TypeError('Failed to fetch');
		}
	};
	await assert.rejects(
		async () => pollJob(mockClient, 'job-offline', { intervalMs: 1 }),
		(err) => {
			assert.equal(err.code, 'POLL_FAILED');
			assert.equal(err.retryable, true);
			assert.match(err.message, /may still be running/);
			return true;
		}
	);
});

test('pollJob rethrows an answer from the server instead of retrying it', async () => {
	let calls = 0;
	const mockClient = {
		async getJob() {
			calls += 1;
			throw Object.assign(new Error('Job not found'), { status: 404 });
		}
	};
	await assert.rejects(async () => pollJob(mockClient, 'job-gone', { intervalMs: 1 }), { status: 404 });
	assert.equal(calls, 1);
});

test('pollJob waits long enough by default for a slow image provider', async () => {
	const realNow = Date.now;
	let now = realNow();
	let calls = 0;
	Date.now = () => now;
	try {
		const mockClient = {
			async getJob(id) {
				calls += 1;
				now += 5 * 60 * 1000; // five minutes pass between polls
				return { id, status: calls < 3 ? JOB_STATUS.RUNNING : JOB_STATUS.DONE };
			}
		};
		const job = await pollJob(mockClient, 'job-slow', { intervalMs: 1 });
		assert.equal(job.status, JOB_STATUS.DONE);
	} finally {
		Date.now = realNow;
	}
});

test('pollJob rides out a busy API (429, 503) instead of failing a healthy job', async () => {
	let calls = 0;
	const mockClient = {
		async getJob(id) {
			calls += 1;
			if (calls === 1) throw Object.assign(new Error('Request limit reached; retry later'), { status: 429 });
			if (calls === 2) throw Object.assign(new Error('Unavailable'), { status: 503 });
			return { id, status: JOB_STATUS.DONE };
		}
	};
	const job = await pollJob(mockClient, 'job-busy', { intervalMs: 1 });
	assert.equal(job.status, JOB_STATUS.DONE);
	assert.equal(calls, 3);
});

test('pollJob polls a long job less often, up to its ceiling', async () => {
	const stamps = [];
	const mockClient = {
		async getJob(id) {
			stamps.push(Date.now());
			return { id, status: stamps.length < 8 ? JOB_STATUS.RUNNING : JOB_STATUS.DONE };
		}
	};
	await pollJob(mockClient, 'job-long', { intervalMs: 4, maxIntervalMs: 20 });
	const gaps = stamps.slice(1).map((t, i) => t - stamps[i]);
	assert.ok(gaps[gaps.length - 1] >= gaps[0], `gaps should not shrink: ${gaps}`);
	assert.ok(Math.max(...gaps) < 60, `gaps stay under the ceiling: ${gaps}`);
});
