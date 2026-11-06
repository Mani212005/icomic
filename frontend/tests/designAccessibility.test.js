import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const css = readFileSync(join(root, 'src/lib/design/theme.css'), 'utf8');
const declarations = Object.fromEntries([...css.matchAll(/--([\w-]+):\s*([^;]+);/g)].map((match) => [match[1], match[2].trim()]));
function colour(name) {
  const value = declarations[name];
  if (/^#[a-f\d]{6}$/i.test(value)) return value.toLowerCase();
  const alias = value.match(/^var\(--([\w-]+)\)$/);
  if (alias) return colour(alias[1]);
  const mix = value.match(/^color-mix\(in srgb, var\(--([\w-]+)\) (\d+)%, var\(--([\w-]+)\)\)$/);
  assert.ok(mix, `Unsupported colour token: ${name}`);
  const first = colour(mix[1]).slice(1).match(/../g).map((hex) => parseInt(hex, 16));
  const second = colour(mix[3]).slice(1).match(/../g).map((hex) => parseInt(hex, 16));
  const amount = Number(mix[2]) / 100;
  return '#' + first.map((value, index) => Math.round(value * amount + second[index] * (1 - amount)).toString(16).padStart(2, '0')).join('');
}
function luminance(hex) {
  const linear = hex.slice(1).match(/../g).map((value) => parseInt(value, 16) / 255)
    .map((value) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
  return linear.reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0);
}
function contrast(first, second) {
  const values = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (values[0] + 0.05) / (values[1] + 0.05);
}

test('the supplied palette and its semantic surfaces retain AA text and control contrast', () => {
  for (const [name, expected] of Object.entries({ primary: '#c47f6f', background: '#f8f4ee', surface: '#fcfaf7', text: '#4b4542', accent: '#d6c19b' })) {
    assert.equal(colour(name), expected);
  }
  for (const background of ['paper', 'sheet', 'sage', 'success-soft', 'warning-soft', 'danger-soft', 'accent-soft', 'accent-hover']) {
    for (const foreground of ['ink', 'muted']) {
      assert.ok(contrast(colour(foreground), colour(background)) >= 4.5, `${foreground} on ${background}`);
    }
  }
  for (const background of ['paper', 'sheet', 'sage']) {
    for (const foreground of ['line', 'focus']) {
      assert.ok(contrast(colour(foreground), colour(background)) >= 3, `${foreground} on ${background}`);
    }
  }
});

test('paper reference stays below 250 KB and other visual assets stay below 100 KB', () => {
  function visit(directory) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) visit(path);
      else assert.ok(statSync(path).size < (entry.name === 'watercolour-paper.webp' ? 250_000 : 100_000), path);
    }
  }
  visit(join(root, 'static'));
});


test('visible UI copy remains within the 534-word ceiling', () => {
  const result = execFileSync('python3', [join(root, '../scripts/count_frontend_text.py'), join(root, 'src')], { encoding: 'utf8' });
  assert.ok(Number(result.match(/copy_words=(\d+)/)[1]) <= 534, result);
});
