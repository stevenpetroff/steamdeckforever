#!/usr/bin/env node
// R16 guard: the built phone page must load nothing from outside the Deck.
// Fails if any shipped text asset contains an absolute http(s):// URL.
//
// Exempt: license files (licenses/*-OFL.txt, OFL.txt), whose text cites the
// OFL's home page, and binary font files, whose name tables carry license and
// designer URLs as inert metadata (never fetched by the browser).
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs';
import { join, relative, extname, basename } from 'node:path';

const URL_RE = /\bhttps?:\/\/[^\s"'`)<>]*/gi;
const BINARY_FONT = new Set(['.ttf', '.otf', '.woff', '.woff2']);

function isLicense(path) {
  const name = basename(path);
  return /(^|-)OFL\.txt$/i.test(name) || /^LICEN[CS]E/i.test(name);
}

function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...walk(p));
    else out.push(p);
  }
  return out;
}

const dir = process.argv[2] ?? 'dist';
if (!existsSync(dir)) {
  console.error(`check:local: ${dir}/ not found — run npm run build first`);
  process.exit(2);
}

const problems = [];
let scanned = 0;
for (const file of walk(dir)) {
  const rel = relative(dir, file);
  if (isLicense(file) || BINARY_FONT.has(extname(file).toLowerCase())) continue;
  scanned++;
  const text = readFileSync(file, 'utf8');
  for (const m of text.matchAll(URL_RE)) problems.push(`${rel}: ${m[0]}`);
}

if (problems.length) {
  console.error('check:local: absolute URLs found in the build output (R16):');
  for (const p of problems) console.error('  ' + p);
  process.exit(1);
}
console.log(`check:local: OK — ${scanned} files in ${dir}/ contain no absolute http(s) URLs`);
