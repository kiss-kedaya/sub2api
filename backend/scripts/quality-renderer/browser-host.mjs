import { chromium } from 'playwright';
import { killTree } from './process-tree.mjs';

let server;
let stopping = false;
let launching = true;
async function stop() {
  if (stopping) return;
  if (launching) {
    // Do not abandon a browser that is between spawn and launchServer resolution.
    process.once('browser-ready', () => void stop());
    return;
  }
  stopping = true;
  const pid = server?.process().pid;
  const hard = setTimeout(() => { killTree(pid); process.exit(1); }, 1500);
  await server?.close().catch(() => {});
  clearTimeout(hard);
  process.exit(0);
}
// IPC disconnect is also delivered when the rendering worker is forcibly killed.
process.on('disconnect', () => void stop());
process.on('message', message => { if (message === 'shutdown') void stop(); });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => void stop());
setTimeout(() => void stop(), 25000);

try {
  if (process.getuid?.() === 0) throw new Error('Root rendering is forbidden');
  server = await chromium.launchServer({
    headless: true,
    chromiumSandbox: true,
    executablePath: process.env.QUALITY_CHROMIUM_EXECUTABLE_PATH || undefined,
    timeout: 10000,
    args: ['--disable-dev-shm-usage', '--disable-background-networking', '--disable-component-update'],
  });
  launching = false;
  process.emit('browser-ready');
  if (!process.connected || stopping) await stop();
  else process.send({ endpoint: server.wsEndpoint(), browserPid: server.process().pid });
} catch {
  launching = false;
  process.emit('browser-ready');
  if (process.connected) process.send({ error: 'Chromium launch failed (sandbox required)' });
  await stop();
}
