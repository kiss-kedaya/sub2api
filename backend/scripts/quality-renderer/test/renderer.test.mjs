import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn, fork } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { fileURLToPath } from 'node:url';
import { mkdir, writeFile } from 'node:fs/promises';
import { PNG } from 'pngjs';
import { CSP, MAX_INPUT, MAX_OUTPUT, prepareHTML, readHTML, encodeFrames } from '../policy.mjs';
import { Readable } from 'node:stream';

const entry = fileURLToPath(new URL('../render.mjs', import.meta.url));
const artwork = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 960 640"><rect width="960" height="640" fill="white"/><rect width="80" height="80" y="120" fill="#e02020"><animate attributeName="x" values="40;760" dur="2s" repeatCount="indefinite"/></rect></svg>';
const root = process.getuid?.() === 0;

function render(html) {
  const child = spawn(process.execPath, [entry], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  const stdout = [];
  const stderr = [];
  child.stdout.on('data', chunk => stdout.push(chunk));
  child.stderr.on('data', chunk => stderr.push(chunk));
  child.stdin.on('error', () => {});
  if (html !== undefined) child.stdin.end(html);
  const done = once(child, 'exit').then(([code, signal]) => ({ code, signal, stdout: Buffer.concat(stdout), stderr: Buffer.concat(stderr).toString() }));
  return { child, done };
}

function framesOf(result) {
  assert.equal(result.code, 0, result.stderr);
  assert.ok(result.stdout.length <= MAX_OUTPUT);
  const frames = JSON.parse(result.stdout.toString());
  assert.equal(frames.length, 4);
  for (const frame of frames) {
    assert.deepEqual(Object.keys(frame), ['time', 'png']);
    assert.ok(Number.isFinite(frame.time));
    assert.match(frame.png, /^[A-Za-z0-9+/]+=*$/);
    const image = PNG.sync.read(Buffer.from(frame.png, 'base64'));
    assert.equal(image.width, 960);
    assert.equal(image.height, 640);
  }
  return frames;
}

function coloredBounds(frame, channel) {
  const image = PNG.sync.read(Buffer.from(frame.png, 'base64'));
  let count = 0;
  let xTotal = 0;
  for (let y = 0; y < image.height; y++) for (let x = 0; x < image.width; x++) {
    const i = (y * image.width + x) * 4;
    if (image.data[i + channel] > 150 && image.data[i + (channel + 1) % 3] < 90 && image.data[i + (channel + 2) % 3] < 90) {
      count++;
      xTotal += x;
    }
  }
  assert.ok(count > 1000, 'frame must contain substantial nonblank artwork');
  return xTotal / count;
}

test('policy rejects active tags, event handlers, dangerous URLs and SMIL', () => {
  const payloads = [
    '<script>fetch("https://example.invalid")</script>', '<SCRIPT src="https://example.invalid/x"></SCRIPT>',
    '<iframe srcdoc="x"></iframe>', '<object></object>', '<embed>', '<form></form>',
    '<meta http-equiv="refresh" content="0;url=https://example.invalid">', '<base href="https://example.invalid">',
    '<meta http-equiv="Content-Security-Policy" content="script-src *">',
    '<link rel="stylesheet" href="https://example.invalid">', '<template><script>x</script></template>',
    '<div ONCLICK="x()"></div>', '<img src="https://example.invalid" onerror="x()">',
    '<x-custom></x-custom>', '<svg onload="x()"></svg>',
    '<svg><foreignObject><div>x</div></foreignObject></svg>',
    '<svg><image href="jav&#x61;script:alert(1)"/></svg>',
    '<svg><image href="data:image/svg+xml;base64,AAAA"/></svg>',
    '<svg><use href="file:///etc/passwd"/></svg>',
    '<svg><animate attributeName="href" values="javascript:alert(1)"/></svg>',
    '<svg><set attributeName="onclick" to="x()"/></svg>',
    '<svg><animate attributeName="x" begin="rect.click"/></svg>',
    '<svg><a href="#x">x</a></svg>', '<math><mtext>x</mtext></math>',
  ];
  for (const payload of payloads) assert.throws(() => prepareHTML(payload + artwork), /Unsafe HTML/);
  assert.throws(() => prepareHTML('<p>no artwork</p>'), /exactly one SVG/);
  assert.throws(() => prepareHTML(artwork + artwork), /exactly one SVG/);
  assert.match(CSP, /script-src 'none'/);
  assert.match(CSP, /worker-src 'none'/);
  assert.match(CSP, /sandbox/);
});

test('passive charset, viewport, theme-color and other named meta are preserved', () => {
  for (const meta of ['<meta charset="utf-8">', '<meta name="viewport" content="width=device-width,initial-scale=1">', '<meta name="theme-color" content="#ffffff">', '<meta name="description" content="SVG artwork">', '<meta name="color-scheme" content="light">']) {
    assert.ok(prepareHTML(meta + artwork).includes(meta));
  }
});

test('input is byte-limited, UTF-8 validated, output includes JSON/base64 overhead', async () => {
  const exact = artwork + ' '.repeat(MAX_INPUT - Buffer.byteLength(artwork));
  assert.doesNotThrow(() => prepareHTML(exact));
  assert.throws(() => prepareHTML(exact + ' '), /1 MiB/);
  assert.throws(() => prepareHTML(Buffer.from([0xff])), /UTF-8/);
  assert.throws(() => prepareHTML(''), /empty/);
  await assert.rejects(readHTML(Readable.from([Buffer.alloc(MAX_INPUT), Buffer.from('x')])), /1 MiB/);
  assert.throws(() => encodeFrames([{ time: 0, png: 'x'.repeat(MAX_OUTPUT) }]), /8 MiB/);
  assert.equal(encodeFrames([{ time: 0, png: 'x' }]), '[{"time":0,"png":"x"}]');
});

test('SMIL four frames are 960x640, nonblank and sampled at exact times', { skip: root }, async () => {
  const frames = framesOf(await render(artwork).done);
  assert.deepEqual(frames.map(frame => frame.time), [0, 0.46, 1.02, 1.58]);
  const centers = frames.map(frame => coloredBounds(frame, 0));
  for (let index = 0; index < centers.length; index++) assert.ok(Math.abs(centers[index] - (79.5 + 720 * [0, 0.23, 0.51, 0.79][index])) < 3);
  assert.equal(new Set(frames.map(frame => frame.png)).size, 4);
  const out = new URL('../test-output/', import.meta.url);
  await mkdir(out, { recursive: true });
  for (let index = 0; index < frames.length; index++) await writeFile(new URL('smil-' + index + '.png', out), Buffer.from(frames[index].png, 'base64'));
});

test('HTML style selectors and CSS animations survive; outside HTML is not captured', { skip: root }, async () => {
  const html = '<style>@keyframes slide{from{transform:translateX(0)}to{transform:translateX(600px)}}.scene .moving{fill:#2020e0;animation:slide 4s linear infinite}.cover{position:fixed;inset:0;background:#00ff00;z-index:2147483647}</style><div class="scene">' +
    '<svg viewBox="0 0 960 640"><rect class="moving" x="40" y="120" width="80" height="80"/></svg></div><div class="cover">NOT ARTWORK</div>';
  const frames = framesOf(await render(html).done);
  assert.deepEqual(frames.map(frame => frame.time), [0, 0.92, 2.04, 3.16]);
  const centers = frames.map(frame => coloredBounds(frame, 2));
  for (let index = 0; index < centers.length; index++) assert.ok(Math.abs(centers[index] - (79.5 + 600 * [0, 0.23, 0.51, 0.79][index])) < 3);
  assert.equal(new Set(frames.map(frame => frame.png)).size, 4);
});

test('no usable duration uses three second cycle', { skip: root }, async () => {
  const frames = framesOf(await render(artwork.replace('dur="2s"', 'dur="100s"')).done);
  assert.deepEqual(frames.map(frame => frame.time), [0, 0.6900000000000001, 1.53, 2.37]);
});

test('HTTP CSS imports/fonts/images never reach a real loopback server', { skip: root }, async () => {
  let requests = 0;
  const server = createServer((request, response) => { requests++; response.end('unexpected'); });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  try {
    const url = 'http://127.0.0.1:' + server.address().port;
    const html = '<style>@import url("' + url + '/css");@font-face{font-family:remote;src:url("' + url + '/font")}svg{background-image:url("' + url + '/bg")}</style>' +
      artwork.replace('</svg>', '<image href="' + url + '/image" width="100" height="100"/><text font-family="remote" x="20" y="30">local</text></svg>') + '<img src="' + url + '/html-image">';
    framesOf(await render(html).done);
    assert.equal(requests, 0);
    const blocked = await render('<script>fetch("' + url + '/script")</script>' + artwork).done;
    assert.equal(blocked.code, 1);
    assert.equal(blocked.stdout.length, 0);
    assert.match(blocked.stderr, /Unsafe HTML/);
    assert.equal(requests, 0);
  } finally { await new Promise(resolve => server.close(resolve)); }
});

test('CLI rejects scripts and oversized stdin with empty stdout', async () => {
  for (const input of ['<script>while(true){}</script>' + artwork, Buffer.alloc(MAX_INPUT + 1, 32)]) {
    const result = await render(input).done;
    assert.equal(result.code, 1);
    assert.equal(result.stdout.length, 0);
    assert.match(result.stderr, root ? /Root rendering/ : /Unsafe HTML|1 MiB/);
  }
});

test('stdin left open still exits within 30 seconds', { skip: root, timeout: 32000 }, async () => {
  const started = Date.now();
  const running = render();
  const result = await running.done;
  assert.equal(result.code, 1);
  assert.equal(result.stdout.length, 0);
  assert.match(result.stderr, /deadline/);
  assert.ok(Date.now() - started < 30000);
});

function alive(pid) {
  try { process.kill(pid, 0); return true; } catch { return false; }
}

test('SIGKILL of rendering worker triggers independent Chromium cleanup', { skip: root, timeout: 15000 }, async () => {
  const child = fork(fileURLToPath(new URL('../worker.mjs', import.meta.url)), [], { stdio: ['pipe', 'ignore', 'ignore', 'ipc'] });
  const exited = once(child, 'exit');
  let pid;
  try {
    const message = once(child, 'message');
    child.stdin.end(artwork);
    const [ready] = await message;
    assert.ok(Number.isInteger(ready.browserPid), JSON.stringify(ready));
    pid = ready.browserPid;
    assert.ok(alive(pid));
    child.kill('SIGKILL');
    await exited;
    const deadline = Date.now() + 5000;
    while (alive(pid) && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 100));
    assert.equal(alive(pid), false, 'Chromium must not survive worker SIGKILL');
  } finally { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); }
});

test('root invocation is rejected before browser launch', { skip: !root }, async () => {
  const result = await render(artwork).done;
  assert.equal(result.code, 1);
  assert.equal(result.stdout.length, 0);
  assert.match(result.stderr, /Root rendering/);
});
