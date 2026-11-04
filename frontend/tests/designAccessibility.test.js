import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const css = readFileSync(join(root, 'src/lib/design/theme.css'), 'utf8');
const tokens = Object.fromEntries([...css.matchAll(/--([\w-]+):\s*(#[a-f\d]{6})\s*;/gi)].map((match) => [match[1], match[2]]));
function luminance(hex) {
  const linear = hex.slice(1).match(/../g).map((value) => parseInt(value, 16) / 255)
    .map((value) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
  return linear.reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0);
}
function contrast(first, second) {
  const values = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (values[0] + 0.05) / (values[1] + 0.05);
}

test('design tokens meet AA text and control contrast across semantic states', () => {
  for (const background of ['paper', 'sheet', 'sage', 'success-soft', 'warning-soft', 'danger-soft']) {
    for (const foreground of ['ink', 'muted']) {
      assert.ok(contrast(tokens[foreground], tokens[background]) >= 4.5, `${foreground} on ${background}`);
    }
  }
  for (const background of ['accent', 'accent-hover', 'ink', 'success', 'warning']) {
    assert.ok(contrast(tokens.sheet, tokens[background]) >= 4.5, `light text on ${background}`);
  }
  for (const background of ['paper', 'sheet', 'sage']) {
    for (const foreground of ['line', 'focus']) {
      assert.ok(contrast(tokens[foreground], tokens[background]) >= 3, `${foreground} on ${background}`);
    }
  }
});

test('every bundled visual asset stays below 100 KB', () => {
  function visit(directory) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) visit(path);
      else assert.ok(statSync(path).size < 100_000, path);
    }
  }
  visit(join(root, 'static'));
});
