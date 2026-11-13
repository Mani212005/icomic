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

// Cancel is pending-only and the server answers honestly. These jobs stay unfinished until the test
// moves them, so the cancel lands while the runner is waiting on the job.
function cancellableJobApi(initial, answer) {
	const state = { status: initial, polls: 0, cancels: [] };
	return {
		state,
		async getJob(id) {
			state.polls += 1;
			return { id, status: state.status };
		},
		async cancelJob(id) {
			state.cancels.push(id);
			if (answer instanceof Error) throw answer;
			if (answer.outcome === 'cancelled') state.status = 'cancelled';
			return { outcome: answer.outcome, job: { id, status: state.status }, refunded: answer.outcome === 'cancelled' ? 0.01 : 0 };
		}
	};
}

async function until(check) {
	for (let i = 0; i < 500 && !check(); i += 1) await new Promise((resolve) => setTimeout(resolve, 2));
	assert.ok(check(), 'condition never became true');
}

// Starts a generation and waits until its receipt makes it cancellable. `done` settles with the run.
async function startCancellable(runner, views) {
	await runner.setProject('project-1');
	const done = runner.run({
		kind: JOB_KIND.PAGE,
		label: 'Draft',
		status: 'Generating',
		entityId: 'page-1',
		start: async () => ({ job: { id: 'job-c', status: 'pending' } })
	});
	await until(() => views.at(-1)?.canCancel);
	return { done };
}

test('cancel is not offered until a receipt names a job, and does nothing before then', async () => {
	const api = cancellableJobApi('pending', { outcome: 'cancelled' });
	const { runner, views } = harness(api);
	await runner.setProject('project-1');
	let receipt;
	const start = new Promise((resolve) => (receipt = resolve));
	const done = runner.run({ kind: JOB_KIND.PAGE, label: 'Draft', status: 'Generating', start: () => start });
	assert.equal(views.at(-1).canCancel, false, 'there is nothing on the server to cancel yet');
	await runner.cancel();
	assert.deepEqual(api.state.cancels, [], 'no request is sent for a job the server does not know');
	api.state.status = 'done';
	receipt({ job: { id: 'job-c', status: 'pending' } });
	await done;
});

test('cancelling a job that has not reached the model stops it, charges nothing and releases the controls', async () => {
	const api = cancellableJobApi('pending', { outcome: 'cancelled' });
	const { runner, views, errors, settled, storage } = harness(api);
	const { done } = await startCancellable(runner, views);
	await runner.cancel();
	await done;
	assert.deepEqual(api.state.cancels, ['job-c']);
	assert.equal(runner.active, false, 'a cancelled job is terminal: the controls unlock');
	assert.equal(views.at(-1).notice, 'Cancelled, nothing was charged.');
	assert.equal(errors.length, 0, 'a cancel is not an error');
	assert.deepEqual(settled, [{ kind: 'refresh' }], 'the balance and the entity are reloaded');
	assert.equal(storage.size(), 0, 'the cancelled job is forgotten');
	const polls = api.state.polls;
	await new Promise((resolve) => setTimeout(resolve, 30));
	assert.equal(api.state.polls, polls, 'polling stops once the job is cancelled');
});

test('cancelling a job already with the model is settling: the controls stay locked until it finishes, and it is charged', async () => {
	const api = cancellableJobApi('running', { outcome: 'settling' });
	const { runner, views, errors, settled } = harness(api);
	const { done } = await startCancellable(runner, views);
	await runner.cancel();
	assert.equal(runner.active, true, 'a settling job is not terminal: the controls stay locked');
	assert.equal(views.at(-1).settling, true);
	assert.equal(views.at(-1).canCancel, false, 'cancel is offered once, then gone');
	assert.equal(views.at(-1).status, 'Already with the model, finishing. You will be charged.');
	assert.ok(!/stopped|cancelled/i.test(views.at(-1).status), 'it never claims the model was stopped');
	await runner.cancel();
	assert.deepEqual(api.state.cancels, ['job-c'], 'a second cancel sends nothing');
	await new Promise((resolve) => setTimeout(resolve, 30));
	assert.equal(runner.active, true);
	assert.equal(views.at(-1).status, 'Already with the model, finishing. You will be charged.', 'polling keeps the settling text');
	assert.equal(settled.length, 0);

	api.state.status = 'done';
	await done;
	assert.equal(runner.active, false, 'only the terminal job unlocks the controls');
	assert.equal(settled.length, 1, 'the finished result is kept and shown');
	assert.equal(settled[0].jobId, 'job-c');
	assert.equal(views.at(-1).notice, 'This was already with the model when you cancelled, so it finished and you were charged.');
	assert.equal(errors.length, 0);
});

test('a job that finished before the cancel landed is reported finished and settles normally', async () => {
	const api = cancellableJobApi('running', { outcome: 'finished' });
	const { runner, views, settled } = harness(api);
	const { done } = await startCancellable(runner, views);
	await runner.cancel();
	assert.equal(views.at(-1).notice, 'This had already finished, so there was nothing to cancel.');
	assert.equal(runner.active, true, 'the poll in flight still reports the final state');
	api.state.status = 'done';
	await done;
	assert.equal(runner.active, false);
	assert.equal(settled.length, 1);
});

test('a failed cancel request keeps the job running and lets the user try again', async () => {
	const api = cancellableJobApi('running', Object.assign(new Error('Failed to fetch'), { status: 0 }));
	const { runner, views, errors } = harness(api);
	const { done } = await startCancellable(runner, views);
	await runner.cancel();
	assert.equal(errors.length, 1);
	assert.equal(runner.active, true, 'nothing is known to have changed');
	assert.equal(views.at(-1).cancelling, false);
	assert.equal(views.at(-1).canCancel, true, 'the user may try again');
	api.state.status = 'done';
	await done;
});

test('a job cancelled elsewhere is reported as cancelled with no charge, not as an error', async () => {
	const api = cancellableJobApi('pending', { outcome: 'cancelled' });
	const { runner, views, errors, settled } = harness(api);
	const { done } = await startCancellable(runner, views);
	api.state.status = 'cancelled'; // another tab cancelled it
	await done;
	assert.equal(runner.active, false);
	assert.equal(views.at(-1).notice, 'Cancelled, nothing was charged.');
	assert.equal(errors.length, 0);
	assert.deepEqual(settled, [{ kind: 'refresh' }]);
});
