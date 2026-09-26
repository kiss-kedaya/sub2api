import { fork } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { CSP, FRACTIONS, MAX_OUTPUT, encodeFrames, readHTML } from './policy.mjs';
import { killTree } from './process-tree.mjs';

let host;
let browser;
let closing;
async function close() {
  closing ??= (async () => {
    await browser?.close().catch(() => {});
    if (host && host.exitCode === null && host.signalCode === null && host.connected) {
      const exited = once(host, 'exit');
      host.send('shutdown', () => {});
      await exited;
    }
  })();
  return closing;
}
function shutdown() { void close().finally(() => process.exit(1)); }
process.on('disconnect', shutdown);
process.on('message', message => { if (message === 'shutdown') shutdown(); });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, shutdown);

async function openBrowser() {
  host = fork(fileURLToPath(new URL('./browser-host.mjs', import.meta.url)), [], { stdio: ['ignore', 'ignore', 'ignore', 'ipc'] });
  const ready = await new Promise((resolve, reject) => {
    host.once('error', reject);
    host.once('exit', () => reject(new Error('Browser guardian exited')));
    host.once('message', message => message.endpoint ? resolve(message) : reject(new Error(message.error)));
  });
  if (process.connected) process.send({ browserPid: ready.browserPid });
  else { killTree(ready.browserPid); throw new Error('Supervisor disconnected'); }
  browser = await chromium.connect(ready.endpoint);
}

async function capture(html) {
  await openBrowser();
  const context = await browser.newContext({
    viewport: { width: 960, height: 640 }, deviceScaleFactor: 1,
    javaScriptEnabled: false, serviceWorkers: 'block', acceptDownloads: false, offline: true,
  });
  const scene = 'https://quality-renderer.invalid/scene';
  let served = false;
  await context.route('**/*', async route => {
    const request = route.request();
    if (!served && request.url() === scene && request.isNavigationRequest() && request.resourceType() === 'document') {
      served = true;
      await route.fulfill({ body: html, contentType: 'text/html; charset=utf-8', headers: { 'Content-Security-Policy': CSP } });
    } else await route.abort('blockedbyclient');
  });
  await context.routeWebSocket('**/*', socket => socket.close());
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  page.on('download', download => void download.cancel());
  context.on('page', popup => { if (popup !== page) void popup.close(); });
  await page.goto(scene, { waitUntil: 'load', timeout: 5000 });
  const cycle = await page.evaluate(() => {
    const svg = document.querySelector('svg');
    if (!svg || document.querySelectorAll('svg').length !== 1) throw new Error('Invalid SVG artwork');
    for (const element of document.body.querySelectorAll('*')) {
      if (element !== svg && !svg.contains(element) && !element.contains(svg)) element.style.setProperty('display', 'none', 'important');
    }
    // Preserve ancestor CSS selectors while normalizing the captured box.
    for (let ancestor = svg.parentElement; ancestor; ancestor = ancestor.parentElement) {
      for (const [name, value] of Object.entries({ transform: 'none', zoom: '1', overflow: 'visible', opacity: '1', filter: 'none', visibility: 'visible', display: 'block' })) ancestor.style.setProperty(name, value, 'important');
    }
    for (const [name, value] of Object.entries({ width: '960px', height: '640px', 'min-width': '960px', 'max-width': '960px', 'min-height': '640px', 'max-height': '640px', position: 'fixed', left: '0px', top: '0px', right: 'auto', bottom: 'auto', margin: '0px', padding: '0px', border: '0px', 'box-sizing': 'border-box', transform: 'none', zoom: '1', display: 'block', overflow: 'hidden', background: 'white', 'z-index': '2147483647' })) svg.style.setProperty(name, value, 'important');
    svg.pauseAnimations();
    svg.setCurrentTime(0);
    const durations = [];
    for (const animation of svg.querySelectorAll('animate, animateTransform, animateMotion, set')) {
      try { durations.push(animation.getSimpleDuration()); } catch { /* Indefinite SMIL has no simple duration. */ }
    }
    for (const animation of document.getAnimations()) {
      animation.pause();
      animation.currentTime = 0;
      if (svg.contains(animation.effect?.target) || animation.effect?.target === svg) durations.push(Number(animation.effect.getTiming().duration) / 1000);
    }
    const usable = durations.filter(value => Number.isFinite(value) && value >= 0.5 && value <= 10);
    return usable.length ? Math.min(...usable) : 3;
  });
  const artwork = page.locator('svg');
  const frames = [];
  let total = 3;
  for (const fraction of FRACTIONS) {
    const time = cycle * fraction;
    await page.evaluate(seconds => {
      document.querySelector('svg').setCurrentTime(seconds);
      for (const animation of document.getAnimations()) {
        animation.pause();
        animation.currentTime = seconds * 1000;
      }
      document.querySelector('svg').getBoundingClientRect();
    }, time);
    const png = await artwork.screenshot({ type: 'png', animations: 'allow', caret: 'hide', timeout: 5000 });
    const frame = { time, png: png.toString('base64') };
    total += Buffer.byteLength(JSON.stringify(frame)) + 1;
    if (total > MAX_OUTPUT) throw new Error('Rendered output exceeds 8 MiB');
    frames.push(frame);
  }
  return encodeFrames(frames);
}

try {
  if (process.getuid?.() === 0) throw new Error('Root rendering is forbidden');
  const html = await readHTML(process.stdin);
  const json = await capture(html);
  await close();
  if (process.connected) process.send({ json }, () => process.exit(0));
  else process.exit(1);
} catch (error) {
  await close();
  const known = /^(?:Unsafe HTML|HTML |Rendered output|Root rendering)/.test(error.message);
  if (process.connected) process.send({ error: known ? error.message : 'Chromium rendering failed (sandbox required)' }, () => process.exit(1));
  else process.exit(1);
}
