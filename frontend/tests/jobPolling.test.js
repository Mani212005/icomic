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
