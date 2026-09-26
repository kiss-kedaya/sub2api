import { readdir, readFile, mkdir, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { PNG } from 'pngjs';

const directory = process.argv[2];
if (!directory) throw new Error('Sample directory is required');
const entry = fileURLToPath(new URL('../render.mjs', import.meta.url));
const names = (await readdir(directory)).filter(name => name.endsWith('.html')).sort();
const results = [];

async function check(name) {
  const raw = await readFile(resolve(directory, name));
  const child = spawn(process.execPath, [entry], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  const output = [];
  const errors = [];
  child.stdout.on('data', chunk => output.push(chunk));
  child.stderr.on('data', chunk => errors.push(chunk));
  child.stdin.on('error', () => {});
  child.stdin.end(raw);
  const [code] = await once(child, 'exit');
  if (code !== 0) return { name, success: false, error: Buffer.concat(errors).toString().trim() };
  try {
    const frames = JSON.parse(Buffer.concat(output).toString());
    if (frames.length !== 4) throw new Error('Invalid frame count');
    for (const frame of frames) {
      const png = PNG.sync.read(Buffer.from(frame.png, 'base64'));
      if (png.width !== 960 || png.height !== 640 || !Number.isFinite(frame.time)) throw new Error('Invalid frame dimensions or time');
    }
    return { name, success: true, frames: frames.length, uniqueFrames: new Set(frames.map(frame => frame.png)).size };
  } catch { return { name, success: false, error: 'Invalid frame protocol' }; }
}

const target = 'a29173-r1409012.html';
if (names.includes(target)) {
  const result = await check(target);
  console.log(JSON.stringify({ target, success: result.success, error: result.error }));
}
// Bound local CPU/memory pressure; each invocation still gets its own browser and guard.
let next = 0;
await Promise.all(Array.from({ length: 2 }, async () => {
  while (next < names.length) results.push(await check(names[next++]));
}));
results.sort((a, b) => a.name.localeCompare(b.name));
const failures = results.filter(result => !result.success);
const summary = { total: results.length, success: results.length - failures.length, failed: failures.length, errors: {} };
const accountSamples = results.filter(result => result.name.startsWith('a29173-'));
summary.accountSamples = { total: accountSamples.length, success: accountSamples.filter(result => result.success).length, failed: accountSamples.filter(result => !result.success).length };
for (const result of failures) summary.errors[result.error] = (summary.errors[result.error] || 0) + 1;
const out = new URL('../test-output/', import.meta.url);
await mkdir(out, { recursive: true });
await writeFile(new URL('batch-results.json', out), JSON.stringify({ summary, results }, null, 2) + '\n');
console.log(JSON.stringify(summary));
