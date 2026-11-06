import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const staticDir = path.join(root, 'static');
const repo = path.resolve(root, '..');

test('favicon, touch icon, and web manifest assets are linked and present', () => {
  const app = readFileSync(path.join(root, 'src/app.html'), 'utf8');
  for (const href of ['/brand/mark.svg', '/favicon-32.png', '/apple-touch-icon.png', '/site.webmanifest']) {
    assert.ok(app.includes(href), `app.html must link ${href}`);
  }
  const manifest = JSON.parse(readFileSync(path.join(staticDir, 'site.webmanifest'), 'utf8'));
  assert.deepEqual(manifest.icons.map(({ sizes }) => sizes), ['192x192', '512x512']);
  for (const file of ['favicon-32.png', 'apple-touch-icon.png', 'icon-192.png', 'icon-512.png']) {
    assert.ok(statSync(path.join(staticDir, file)).size > 100, `${file} must exist`);
  }
  for (const file of ['brand/mark.svg']) {
    const svg = readFileSync(path.join(staticDir, file), 'utf8');
    assert.doesNotMatch(svg, /(?:href|src)=["']https?:\/\//, `${file} must not request a remote resource`);
    execFileSync('python3', ['-c', 'import sys, xml.etree.ElementTree as ET; ET.parse(sys.argv[1])', path.join(staticDir, file)]);
  }
});

test('brand SVG concepts stay valid, self-contained, and under the size budget', () => {
  const brandDir = path.join(repo, 'docs/assets/brand');
  const files = readdirSync(brandDir).filter((name) => name.endsWith('.svg'));
  assert.equal(files.length, 15);
  for (const name of files) {
    const svg = readFileSync(path.join(brandDir, name), 'utf8');
    assert.ok(svg.startsWith('<svg '), `${name} must start with an SVG root`);
    assert.ok(svg.trimEnd().endsWith('</svg>'), `${name} must close its SVG root`);
    assert.ok(Buffer.byteLength(svg) < 30 * 1024, `${name} exceeds 30 KB`);
    assert.doesNotMatch(svg, /(?:href|src)=["']https?:\/\//, `${name} must not make external requests`);
    execFileSync('python3', ['-c', 'import sys, xml.etree.ElementTree as ET; ET.parse(sys.argv[1])', path.join(brandDir, name)]);
  }
});
