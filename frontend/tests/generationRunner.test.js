import test from 'node:test';
import assert from 'node:assert/strict';
import { GenerationRunner, JOB_KIND } from '../src/lib/editor/generationRunner.js';
import { recallActiveJob } from '../src/lib/api/activeJobs.js';

function memoryStorage() {
	const items = new Map();
	return {
		getItem: (key) => (items.has(key) ? items.get(key) : null),
		setItem: (key, value) => items.set(key, String(value)),
		removeItem: (key) => items.delete(key),
		size: () => items.size
	};
}

// A job that moves pending -> running -> done as the client polls it.
function slowJobApi(steps = ['pending', 'running', 'running', 'done']) {
	const polls = [];
	return {
		polls,
		async getJob(id) {
			polls.push(id);
			return { id, status: steps[Math.min(polls.length - 1, steps.length - 1)] };
		}
	};
}

function harness(api, storage = memoryStorage()) {
	const views = [];
	const errors = [];
	const settled = [];
	const runner = new GenerationRunner({
		api,
		storage,
		intervalMs: 1,
		settle: async (job) => void settled.push(job),
		onChange: (view) => views.push(view),
		onError: (err) => errors.push(err)
	});
	return { runner, views, errors, settled, storage };
}

test('a receipt is followed by job ID with visible progress, then settles the finished entity', async () => {
	const api = slowJobApi();
	const { runner, views, settled, errors, storage } = harness(api);
	await runner.setProject('project-1');
	let started = 0;
	await runner.run({
		kind: JOB_KIND.PAGE,
		label: 'Draft',
		status: 'Generating',
		entityId: 'page-1',
		start: async (key) => {
			started += 1;
			assert.ok(key, 'every action carries an idempotency key');
			return { job: { id: 'job-1', status: 'pending' }, entity_type: 'page', entity_id: 'page-1' };
		}
	});
	assert.equal(started, 1);
	assert.deepEqual(api.polls, ['job-1', 'job-1', 'job-1', 'job-1']);
	assert.deepEqual(
		views.map((v) => v.status).filter((s) => ['Queued', 'Generating'].includes(s)),
		['Generating', 'Queued', 'Generating', 'Generating'], // the click, then pending, running, running
		'the banner follows the job: queued, then generating'
	);
	assert.deepEqual(settled, [{ jobId: 'job-1', kind: JOB_KIND.PAGE, label: 'Draft', entityId: 'page-1' }]);
	assert.equal(runner.active, false);
	assert.equal(errors.length, 0);
	assert.equal(storage.size(), 0, 'a settled job is forgotten');
});

test('controls stay locked from the click until the job settles, so a second click starts nothing', async () => {
	let release;
	const gate = new Promise((resolve) => (release = resolve));
	const api = {
		async getJob(id) {
			await gate;
			return { id, status: 'done' };
		}
	};
	const { runner } = harness(api);
	await runner.setProject('project-1');
	let starts = 0;
	const start = async () => {
		starts += 1;
		return { job: { id: 'job-1' } };
	};
	const first = runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start });
	await runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start }); // the duplicate click
	assert.equal(runner.active, true);
	assert.equal(starts, 1);
	release();
	await first;
	assert.equal(runner.active, false);
});

test('a reload resumes the remembered job by ID without submitting another generation', async () => {
	const storage = memoryStorage();
	// First page load: the receipt arrives, then the tab is closed while the provider is still working.
	const before = harness({ getJob: () => new Promise(() => {}) }, storage);
	await before.runner.setProject('project-1');
	void before.runner.run({
		kind: JOB_KIND.CHARACTER,
		label: 'Reference',
		status: 'Rendering',
		entityId: 'char-1',
		start: async () => ({ job: { id: 'job-77', status: 'running' } })
	});
	await new Promise((resolve) => setTimeout(resolve, 10));
	assert.equal(recallActiveJob('project-1', storage).jobId, 'job-77');

	// Second load: the job finished meanwhile. Nothing is submitted; the same job is followed to its end.
	const api = slowJobApi(['done']);
	const after = harness(api, storage);
	await after.runner.setProject('project-1');
	assert.deepEqual(api.polls, ['job-77']);
	assert.deepEqual(after.settled, [{ jobId: 'job-77', kind: JOB_KIND.CHARACTER, label: 'Reference', entityId: 'char-1', savedAt: after.settled[0].savedAt }]);
	assert.equal(after.runner.active, false);
	assert.equal(storage.size(), 0);
});

test('a job remembered for another project is not resumed', async () => {
	const storage = memoryStorage();
	const first = harness({ getJob: () => new Promise(() => {}) }, storage);
	await first.runner.setProject('project-1');
	void first.runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start: async () => ({ job: { id: 'job-1' } }) });
	await new Promise((resolve) => setTimeout(resolve, 10));

	const api = slowJobApi(['done']);
	const other = harness(api, storage);
	await other.runner.setProject('project-2');
	assert.deepEqual(api.polls, []);
	assert.equal(other.runner.active, false);
});

test('a slow job pauses the wait but keeps the controls locked, and Keep waiting follows the same job', async () => {
	let calls = 0;
	const api = {
		async getJob(id) {
			calls += 1;
			if (calls <= 5) throw new TypeError('Failed to fetch'); // the tunnel is down
			return { id, status: 'done' };
		}
	};
	const { runner, views, settled, errors, storage } = harness(api);
	await runner.setProject('project-1');
	await runner.run({ kind: JOB_KIND.PAGE, label: 'Paint', status: 'Generating', entityId: 'page-1', start: async () => ({ job: { id: 'job-9' } }) });
	assert.equal(runner.active, true, 'the job is still running: a lost connection must not reopen the click window');
	assert.match(views.at(-1).stalled, /Lost contact/);
	assert.equal(errors.length, 0);
	assert.equal(recallActiveJob('project-1', storage).jobId, 'job-9');

	await runner.follow();
	assert.equal(runner.active, false);
	assert.equal(settled.length, 1);
	assert.equal(settled[0].jobId, 'job-9');
});

test('a failed job releases the controls and reports the error once', async () => {
	const api = { async getJob(id) { return { id, status: 'failed', error_message: 'Generation was rejected.', error_code: 'CONTENT_POLICY_REJECTED' }; } };
	const { runner, errors, settled } = harness(api);
	await runner.setProject('project-1');
	await runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start: async () => ({ job: { id: 'job-3' } }) });
	assert.equal(runner.active, false);
	assert.equal(errors.length, 1);
	assert.equal(errors[0].code, 'CONTENT_POLICY_REJECTED');
	assert.deepEqual(settled, [{ kind: 'refresh' }], 'credits and entities are reloaded after a failure');
});

test('an uncertain job is reported as unknown, never as a result to retry blindly', async () => {
	const api = { async getJob(id) { return { id, status: 'uncertain', error_message: 'The generation outcome could not be confirmed.' }; } };
	const { runner, errors } = harness(api);
	await runner.setProject('project-1');
	await runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start: async () => ({ job: { id: 'job-4' } }) });
	assert.equal(errors[0].code, 'OUTCOME_UNKNOWN');
	assert.equal(runner.active, false);
});

test('a request that never produced a receipt releases the controls with the error', async () => {
	const { runner, errors } = harness({ getJob: async () => assert.fail('no job to poll') });
	await runner.setProject('project-1');
	await runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start: async () => { throw Object.assign(new Error('Not enough credits'), { status: 402 }); } });
	assert.equal(runner.active, false);
	assert.equal(errors[0].status, 402);
});

test('a remembered job the server no longer knows is dropped quietly', async () => {
	const storage = memoryStorage();
	storage.setItem('icomic.activeJob.project-1', JSON.stringify({ jobId: 'gone', kind: JOB_KIND.PAGE, label: 'Draft', entityId: 'page-1', savedAt: Date.now() }));
	const api = { async getJob() { throw Object.assign(new Error('Job not found'), { status: 404 }); } };
	const { runner, errors } = harness(api, storage);
	await runner.setProject('project-1');
	assert.equal(errors.length, 0);
	assert.equal(runner.active, false);
	assert.equal(storage.size(), 0);
});
