// R16: the page loads every asset from the Deck. No absolute http(s) URLs may
// appear in shipped source (license text excepted).
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const root = process.cwd(); // phone/
const URL_RE = /\bhttps?:\/\/[^\s"'`)<>]*/gi;

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

describe('local only (R16)', () => {
  it('index.html and shipped src files contain no absolute http(s) URLs', () => {
    const files = [
      resolve(root, 'index.html'),
      ...walk(resolve(root, 'src')).filter((f) => /\.(ts|css|html|json|svg)$/.test(f) && !f.endsWith('.test.ts')),
    ];
    expect(files.length).toBeGreaterThan(5);
    const hits = files.flatMap((f) =>
      [...readFileSync(f, 'utf8').matchAll(URL_RE)].map((m) => `${f}: ${m[0]}`),
    );
    expect(hits).toEqual([]);
  });

  it('index.html references only relative assets', () => {
    const html = readFileSync(resolve(root, 'index.html'), 'utf8');
    for (const m of html.matchAll(/\b(?:src|href)\s*=\s*"([^"]*)"/g)) {
      expect(m[1], m[0]).not.toMatch(/^(?:[a-z]+:)?\/\//i);
    }
  });

  it('the scanner flags absolute URLs', () => {
    const tmp = resolve(root, 'node_modules/.cache/check-local-fixture');
    execFileSync('mkdir', ['-p', tmp]);
    execFileSync('sh', ['-c', `printf 'x="https://cdn.example/f.woff2"' > ${tmp}/a.js`]);
    let failed = false;
    try {
      execFileSync('node', [resolve(root, 'scripts/check-local.mjs'), tmp], { stdio: 'pipe' });
    } catch {
      failed = true;
    }
    expect(failed).toBe(true);
  });

  it.skipIf(!existsSync(resolve(root, 'dist')))('the built dist/ passes check:local', () => {
    const out = execFileSync('node', [resolve(root, 'scripts/check-local.mjs'), resolve(root, 'dist')], {
      encoding: 'utf8',
    });
    expect(out).toMatch(/OK/);
  });
});
