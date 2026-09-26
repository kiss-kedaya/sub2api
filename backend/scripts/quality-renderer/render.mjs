import { fork } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { killTree } from './process-tree.mjs';

if (process.getuid?.() === 0) {
  process.stderr.write('Root rendering is forbidden; use a non-root isolated user\n');
  process.exit(1);
}
// Parsing and Chromium live in separate processes, so they cannot block this deadline.
const worker = fork(fileURLToPath(new URL('./worker.mjs', import.meta.url)), [], {
  stdio: ['inherit', 'ignore', 'ignore', 'ipc'],
  detached: process.platform !== 'win32',
});
let browserPid;
let result;
let failure;
let finished = false;

function terminate(message) {
  if (finished) return;
  finished = true;
  killTree(browserPid);
  killTree(worker.pid);
  process.stderr.write(message + '\n');
  process.exit(1);
}

const softTimeout = setTimeout(() => {
  failure = 'Renderer exceeded 30 second deadline';
  if (worker.connected) worker.send('shutdown', () => {});
}, 26500);
const hardTimeout = setTimeout(() => terminate('Renderer exceeded 30 second deadline'), 27500);
const finalTimeout = setTimeout(() => process.exit(1), 30000);

worker.on('message', message => {
  if (Number.isInteger(message.browserPid)) browserPid = message.browserPid;
  if (typeof message.error === 'string') failure = message.error;
  if (typeof message.json === 'string' && Buffer.byteLength(message.json) + 1 <= 8 * 1024 * 1024) result = message.json;
});
worker.on('error', () => terminate('Renderer worker failed'));
worker.on('exit', code => {
  if (finished) return;
  finished = true;
  if (failure || code !== 0 || !result) {
    killTree(browserPid);
    process.stderr.write((failure || 'Renderer worker failed') + '\n');
    process.exit(1);
  }
  // Keep the deadline armed until the Go caller drains stdout.
  clearTimeout(softTimeout);
  clearTimeout(hardTimeout);
  process.stdout.write(result + '\n', () => { clearTimeout(finalTimeout); process.exit(0); });
});
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => terminate('Renderer interrupted'));
