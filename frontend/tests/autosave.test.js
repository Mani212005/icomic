import test from 'node:test';
import assert from 'node:assert/strict';
import { Autosaver, SAVE } from '../src/lib/editor/autosave.js';

// A save whose completion the test controls, so a slow earlier request can be overtaken by newer edits.
function controlledSaves() {
	const requests = [];
	let running = 0;
	let maxRunning = 0;
	const save = (value, saved) => {
		running += 1;
		maxRunning = Math.max(maxRunning, running);
		return new Promise((resolve, reject) => {
			requests.push({ value, saved, resolve: () => (running--, resolve()), reject: (error) => (running--, reject(error)) });
		});
	};
	return { save, requests, maxRunning: () => maxRunning };
}

const tick = () => new Promise((resolve) => setImmediate(resolve));
const states = () => {
	const seen = [];
	return { seen, onChange: (state) => seen.push(state) };
};

test('edits are saved once, after the delay, with the latest value and what was stored before', async () => {
	const { save, requests } = controlledSaves();
	const { seen, onChange } = states();
	const saver = new Autosaver({ save, saved: { text: 'a' }, delay: 20, onChange });
	saver.update({ text: 'ab' });
	saver.update({ text: 'abc' });
	assert.equal(requests.length, 0, 'nothing is sent while typing');
	await new Promise((resolve) => setTimeout(resolve, 40));
	assert.equal(requests.length, 1);
	assert.deepEqual(requests[0].value, { text: 'abc' });
	assert.deepEqual(requests[0].saved, { text: 'a' });
	requests[0].resolve();
	await saver.flush();
	assert.deepEqual(seen, [SAVE.DIRTY, SAVE.SAVING, SAVE.SAVED]);
});

test('a slow earlier save can never overwrite a newer edit: saves never overlap and run in edit order', async () => {
	const { save, requests, maxRunning } = controlledSaves();
	const { seen, onChange } = states();
	const saver = new Autosaver({ save, saved: 'start', delay: 5, onChange });
	saver.update('first');
	const firstFlush = saver.flush();
	await tick();
	assert.equal(requests.length, 1);

	// While the first request is still in flight the user keeps typing and a second flush is asked for.
	saver.update('second');
	const secondFlush = saver.flush();
	saver.update('third');
	await tick();
	assert.equal(requests.length, 1, 'the second value is not sent while the first is in flight');
	assert.notEqual(saver.state, SAVE.SAVED);

	requests[0].resolve();
	await tick();
	assert.equal(requests.length, 2, 'the next request carries the latest value');
	assert.equal(requests[1].value, 'third');
	assert.equal(requests[1].saved, 'first', 'and knows what the first request stored');
	assert.notEqual(saver.state, SAVE.SAVED, 'the first answer must not mark the newer edits as saved');

	requests[1].resolve();
	await Promise.all([firstFlush, secondFlush]);
	assert.equal(saver.state, SAVE.SAVED);
	assert.equal(saver.saved, 'third');
	assert.equal(maxRunning(), 1);
	assert.equal(seen.at(-1), SAVE.SAVED);
	assert.equal(seen.filter((state) => state === SAVE.SAVED).length, 1, 'saved is reported once, after the last request');
});

test('a failed save keeps the edits, reports the error and is retried by retry() or the next edit', async () => {
	const { save, requests } = controlledSaves();
	const { onChange } = states();
	const saver = new Autosaver({ save, saved: 'a', delay: 1000, onChange });
	saver.update('b');
	const flushed = saver.flush();
	await tick();
	const failure = new Error('network down');
	requests[0].reject(failure);
	await assert.rejects(flushed, /network down/);
	assert.equal(saver.state, SAVE.ERROR);
	assert.equal(saver.error, failure);
	assert.equal(saver.saved, 'a', 'a failed save stores nothing');

	const retried = saver.retry();
	await tick();
	assert.equal(requests.length, 2);
	assert.equal(requests[1].value, 'b');
	requests[1].resolve();
	await retried;
	assert.equal(saver.state, SAVE.SAVED);
});

test('typing again after a failure supersedes the failed value', async () => {
	const { save, requests } = controlledSaves();
	const saver = new Autosaver({ save, saved: 'a', delay: 5 });
	saver.update('b');
	const flushed = saver.flush();
	await tick();
	requests[0].reject(new Error('boom'));
	await assert.rejects(flushed);
	saver.update('c');
	assert.equal(saver.state, SAVE.DIRTY);
	await new Promise((resolve) => setTimeout(resolve, 20));
	assert.equal(requests.length, 2);
	assert.equal(requests[1].value, 'c');
	requests[1].resolve();
	await saver.flush();
	assert.equal(saver.saved, 'c');
});

test('going back to the stored value cancels the pending save', async () => {
	const { save, requests } = controlledSaves();
	const saver = new Autosaver({ save, saved: { n: 1 }, delay: 5 });
	saver.update({ n: 2 });
	saver.update({ n: 1 });
	await new Promise((resolve) => setTimeout(resolve, 20));
	assert.equal(requests.length, 0);
	await saver.flush();
	assert.equal(requests.length, 0, 'flush with nothing changed sends nothing');
});

test('dispose sends the pending edits and then stops reporting', async () => {
	const { save, requests } = controlledSaves();
	const { seen, onChange } = states();
	const saver = new Autosaver({ save, saved: 'a', delay: 1000, onChange });
	saver.update('b');
	const done = saver.dispose();
	await tick();
	assert.equal(requests.length, 1);
	requests[0].resolve();
	await done;
	const count = seen.length;
	saver.update('c');
	assert.equal(seen.length, count);
	assert.equal(requests.length, 1, 'a disposed saver saves nothing more');
});
