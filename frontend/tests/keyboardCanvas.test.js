import test from 'node:test';
import assert from 'node:assert/strict';
import { keyboardCanvas } from '../src/lib/colour/keyboardCanvas.js';

function fixture() {
  const canvas = new EventTarget();
  canvas.width = 600; canvas.height = 750;
  canvas.getBoundingClientRect = () => ({ left: 20, top: 40, width: 240, height: 300 });
  const events = [];
  let brush = false;
  const action = keyboardCanvas(canvas, {
    undo: () => events.push(['undo']), redo: () => events.push(['redo']),
    apply: (point) => events.push(['apply', point]),
    start: (point) => events.push(['start', point]),
    move: (point) => events.push(['move', point]),
    stop: () => events.push(['stop']),
    cursor: (point) => events.push(['cursor', point]),
    isBrush: () => brush
  });
  function key(value, options = {}) {
    const event = new Event('keydown', { cancelable: true });
    Object.defineProperty(event, 'key', { value });
    for (const [name, value] of Object.entries(options)) Object.defineProperty(event, name, { value });
    canvas.dispatchEvent(event);
    return event.defaultPrevented;
  }
  return { canvas, events, action, key, brush: () => brush = true };
}

test('keyboard painting scales to the displayed canvas and leaves Tab navigation free', () => {
  const f = fixture();
  assert.equal(f.key('Tab'), false);
  assert.equal(f.key('ArrowRight'), true);
  f.key('Enter');
  assert.deepEqual(f.events.find(([kind]) => kind === 'apply'), ['apply', { clientX: 144, clientY: 190 }]);
  assert.ok(f.events.some(([kind, point]) => kind === 'cursor' && point.x > 50));
  f.action.destroy();
  const count = f.events.length;
  f.key('Enter');
  assert.equal(f.events.length, count);
});

test('keyboard gap closing starts, draws and stops with Enter, arrows and blur', () => {
  const f = fixture(); f.brush();
  f.key('Enter'); f.key('ArrowDown'); f.key('Enter');
  assert.deepEqual(f.events.filter(([kind]) => kind !== 'cursor').map(([kind]) => kind), ['start', 'move', 'stop']);
  f.key('Enter');
  f.canvas.dispatchEvent(new Event('blur'));
  assert.deepEqual(f.events.slice(-2), [['stop'], ['cursor', null]]);
  f.action.destroy();
});


test('canvas history shortcuts support Control, Command, Shift+Z and Y', () => {
  const f = fixture();
  f.key('z', { ctrlKey: true });
  f.key('Z', { metaKey: true, shiftKey: true });
  f.key('y', { ctrlKey: true });
  assert.deepEqual(f.events.map(([kind]) => kind), ['undo', 'redo', 'redo']);
  f.action.destroy();
});
