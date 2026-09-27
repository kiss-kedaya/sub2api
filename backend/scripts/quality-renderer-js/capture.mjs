// Four-frame capture for artwork whose animation lives in JavaScript.
//
// Reads UTF-8 HTML on stdin, writes [{time, png}] JSON on stdout.
// Loads clock.mjs as an init script so the artwork's rAF/timer/SMIL/WA
// timelines are ours before any page script runs.
//
// Run inside a network-isolated container. See README.md for the flags.
import { readFileSync } from 'node:fs';
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';

const CLOCK = fileURLToPath(new URL('./clock.mjs', import.meta.url));
const FRACTIONS = [0, 0.23, 0.51, 0.79];
const VIEWPORT = { width: 960, height: 640 };
// Cycles shorter than this are ignored when picking the sampling period;
// longer than the upper bound is treated as "not a loop".
const CYCLE_MIN = 0.5;
const CYCLE_MAX = 10;
const CYCLE_FALLBACK = 3;

const html = readFileSync(0, 'utf8');
if (!html.trim()) {
  process.stderr.write('empty input\n');
  process.exit(1);
}

// Inside the container the Chromium setuid sandbox is unavailable (it needs a
// working userns/namespace setup the container does not provide), so this
// renderer runs with --no-sandbox. That is deliberate and is NOT a regression
// of the in-process renderer: the isolation boundary here is the container
// (no outbound network, dropped capabilities, resource limits, one-shot run),
// so the browser sandbox is replaced by the container sandbox, not removed.
// ARTWORK JS MUST ONLY EVER RUN BEHIND THIS BOUNDARY.
const browser = await chromium.launch({
  headless: true,
  chromiumSandbox: false,
  executablePath: process.env.QUALITY_CHROMIUM_EXECUTABLE_PATH || undefined,
  args: [
    '--no-sandbox',
    '--disable-dev-shm-usage',
    '--disable-gpu',
    '--disable-background-networking',
    '--disable-component-update',
    '--no-first-run',
    '--no-default-browser-check',
  ],
});

try {
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: 1,
    javaScriptEnabled: true,
    serviceWorkers: 'block',
    acceptDownloads: false,
    offline: true,
  });
  await context.addInitScript({ path: CLOCK });

  // Outbound requests are denied at the network layer. The artwork is loaded
  // with setContent rather than goto on purpose: an init script survives
  // setContent, but a goto that creates a fresh document drops it and the
  // clock handle disappears.
  await context.route('**/*', route => route.abort('blockedbyclient'));
  await context.routeWebSocket('**/*', socket => socket.close());

  const page = await context.newPage();
  page.setDefaultTimeout(8000);
  page.on('download', d => void d.cancel());
  context.on('page', popup => { if (popup !== page) void popup.close(); });

  // Inline script is allowed by design (that is the point of this renderer);
  // the CSP still denies every other origin, and the container has no route out.
  await page.setContent(html, { waitUntil: 'load', timeout: 8000 });

  // Let the artwork's own setup code run once (DOMContentLoaded handlers etc.)
  // while everything is still frozen.
  await page.waitForTimeout(120);

  const cycle = await page.evaluate(() => {
    const svgs = [...document.querySelectorAll('svg')];
    if (!svgs.length) throw new Error('Invalid SVG artwork');
    // 取面积最大的 SVG 作为主作品，其余（内联图标、装饰、defs sprite）隐藏。
    // 之前要求"全文档只能有一个 SVG"，会把带图标的正常作品整个误拒。
    const area = (s) => { const r = s.getBoundingClientRect(); return r.width * r.height; };
    const svg = svgs.reduce((a, b) => (area(b) > area(a) ? b : a));
    svg.setAttribute('data-qr-main', '1');
    for (const other of svgs) {
      if (other !== svg) other.style.setProperty('display', 'none', 'important');
    }
    for (const el of document.body.querySelectorAll('*')) {
      if (el !== svg && !svg.contains(el) && !el.contains(svg)) {
        el.style.setProperty('display', 'none', 'important');
      }
    }
    for (let a = svg.parentElement; a; a = a.parentElement) {
      for (const [k, v] of Object.entries({ transform: 'none', zoom: '1', overflow: 'visible', opacity: '1', filter: 'none', visibility: 'visible', display: 'block' })) {
        a.style.setProperty(k, v, 'important');
      }
    }
    for (const [k, v] of Object.entries({
      width: '960px', height: '640px', 'min-width': '960px', 'max-width': '960px',
      'min-height': '640px', 'max-height': '640px', position: 'fixed', left: '0px', top: '0px',
      right: 'auto', bottom: 'auto', margin: '0px', padding: '0px', border: '0px',
      'box-sizing': 'border-box', transform: 'none', zoom: '1', display: 'block',
      overflow: 'hidden', background: 'white', 'z-index': '2147483647',
    })) svg.style.setProperty(k, v, 'important');
    // Declared SMIL / CSS durations bound the sampling period.
    const durations = [];
    for (const a of svg.querySelectorAll('animate, animateTransform, animateMotion, set')) {
      try { durations.push(a.getSimpleDuration()); } catch { /* indefinite */ }
    }
    for (const a of document.getAnimations()) {
      try { durations.push(Number(a.effect.getTiming().duration) / 1000); } catch { /* no timing */ }
    }
    const usable = durations.filter(v => Number.isFinite(v) && v >= 0.5 && v <= 10);
    return usable.length ? Math.min(...usable) : 3;
  });

  const bounded = cycle >= CYCLE_MIN && cycle <= CYCLE_MAX ? cycle : CYCLE_FALLBACK;
  const artwork = page.locator('svg[data-qr-main]');
  const frames = [];
  for (const fraction of FRACTIONS) {
    const ms = bounded * fraction * 1000;
    // Reset to zero, let rAF/timer driven artwork run forward to the instant,
    // then absolutely seek SMIL / Web Animations to the same time. The final
    // seek does not disturb the JS positions, which are already at `ms`.
    await page.evaluate(async target => {
      const clock = window.__plateauClock;
      if (!clock) throw new Error('clock missing');
      clock.seek(0);
      if (target > 0) await clock.advance(target);
      clock.seek(target);
    }, ms);
    const png = await artwork.screenshot({ type: 'png', animations: 'allow', caret: 'hide', timeout: 8000 });
    frames.push({ time: Number((bounded * fraction).toFixed(4)), png: png.toString('base64') });
  }

  process.stdout.write(JSON.stringify(frames) + '\n');
} catch (error) {
  // Playwright 会把 evaluate 内抛出的错误前缀成 "page.evaluate: Error: ..."，
  // 不能锚定行首，否则已知错误全部落进"未知异常"分类。
  const known = /Invalid SVG artwork|clock missing/.test(error?.message || '');
  const detail = process.env.QR_DEBUG === '1' ? '\n' + String(error?.stack || error) : '';
  process.stderr.write((known ? error.message : 'JS renderer failed (see container isolation)') + '\n' + detail);
  process.exitCode = 1;
} finally {
  await browser.close().catch(() => {});
}
