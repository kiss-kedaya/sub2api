// Freeze/advance controller for untrusted JS animation, adapted from
// manxue-ai's public preview controller (Apache-2.0).
//
// The artwork does NOT drive its own clock. Every rAF / timer / Web Animation /
// SMIL timeline is captured here, so the harness can drop it at an exact time
// and screenshot that instant.
//
// Installed as an init script BEFORE any page script runs, so the artwork only
// ever sees the wrapped APIs. The exposed handle is defined as a non-writable,
// non-enumerable property, which keeps it invisible to page script in the
// isolated main world while the harness (running in an isolated world) reads it.
(() => {
  'use strict';

  const nativeNow = performance.now.bind(performance);
  const nativeRAF = window.requestAnimationFrame.bind(window);
  const nativeCancelRAF = window.cancelAnimationFrame.bind(window);
  const nativeTimeout = window.setTimeout.bind(window);
  const nativeClearTimeout = window.clearTimeout.bind(window);
  const nativeClearInterval = window.clearInterval.bind(window);
  const nativeDateNow = Date.now.bind(Date);

  const frames = new Map();
  const timers = new Map();
  const pausedAnimations = new Set();
  const pausedSVGs = new Set();
  let nextID = -1;
  let playing = false;
  let virtualNow = 0;
  const dateOrigin = nativeDateNow();

  // CSS animations start paused so a freshly built DOM never races the capture.
  const style = document.createElement('style');
  style.textContent = '*,*::before,*::after{animation-play-state:paused!important}';
  (document.head || document.documentElement).appendChild(style);

  // ---------- clock ----------
  performance.now = () => virtualNow;
  Date.now = () => dateOrigin + virtualNow;

  // During a stepped advance the pump drives a microtask queue instead of real
  // frames, so a 3 second seek costs microseconds rather than 3 wall seconds.
  let pumpQueue = null;

  // ---------- requestAnimationFrame ----------
  function scheduleFrame(id, frame) {
    const run = () => {
      frame.native = null;
      if (!playing || !frames.has(id)) return;
      frames.delete(id);
      frame.callback(virtualNow);
    };
    if (pumpQueue) {
      frame.native = -1;
      pumpQueue.push(run);
      return;
    }
    frame.native = nativeRAF(run);
  }
  window.requestAnimationFrame = callback => {
    const id = nextID--;
    frames.set(id, { callback, native: null });
    if (playing) scheduleFrame(id, frames.get(id));
    return id;
  };
  window.cancelAnimationFrame = id => {
    const frame = frames.get(id);
    if (frame) {
      if (frame.native !== null) nativeCancelRAF(frame.native);
      frames.delete(id);
    } else nativeCancelRAF(id);
  };

  // ---------- setTimeout / setInterval ----------
  function scheduleTimer(id, timer) {
    const run = () => {
      timer.native = null;
      if (!playing || !timers.has(id)) return;
      if (!timer.repeat) timers.delete(id);
      timer.remaining = timer.delay;
      try {
        if (typeof timer.callback === 'function') timer.callback.apply(window, timer.args);
        else (0, eval)(String(timer.callback));
      } finally {
        if (timer.repeat && playing && timers.has(id)) scheduleTimer(id, timer);
      }
    };
    if (pumpQueue) {
      // Timers fire once per step regardless of delay; the artwork only ever
      // observes the virtual clock, so this keeps ordering stable and cheap.
      timer.started = virtualNow;
      timer.native = -1;
      pumpQueue.push(run);
      return;
    }
    timer.started = virtualNow;
    timer.native = nativeTimeout(run, timer.remaining);
  }
  function setTimer(callback, delay, args, repeat) {
    delay = Number(delay) || 0;
    delay = delay > 2147483647 ? 1 : Math.max(0, delay);
    const id = nextID--;
    const timer = { callback, delay, remaining: delay, args, repeat, started: 0, native: null };
    timers.set(id, timer);
    if (playing) scheduleTimer(id, timer);
    return id;
  }
  window.setTimeout = (cb, delay = 0, ...args) => setTimer(cb, delay, args, false);
  window.setInterval = (cb, delay = 0, ...args) => setTimer(cb, delay, args, true);
  window.clearTimeout = window.clearInterval = id => {
    const timer = timers.get(id);
    if (timer) {
      if (timer.native !== null) nativeClearTimeout(timer.native);
      timers.delete(id);
    } else {
      nativeClearTimeout(id);
      nativeClearInterval(id);
    }
  };

  // ---------- Web Animations ----------
  const proto = window.Animation && window.Animation.prototype;
  const nativePlay = proto && proto.play;
  const nativePause = proto && proto.pause;
  function pauseAnimation(animation) {
    if (!nativePause || animation.playState === 'finished' || animation.playState === 'idle') return;
    if (animation.playState === 'running') {
      pausedAnimations.add(animation);
      try { nativePause.call(animation); } catch (_) { pausedAnimations.delete(animation); }
    }
  }
  if (proto) {
    for (const method of ['play', 'reverse']) {
      const native = proto[method];
      if (!native) continue;
      proto[method] = function (...a) {
        const r = native.apply(this, a);
        if (!playing) pauseAnimation(this);
        return r;
      };
    }
    for (const method of ['pause', 'cancel', 'finish']) {
      const native = proto[method];
      if (!native) continue;
      proto[method] = function (...a) {
        pausedAnimations.delete(this);
        return native.apply(this, a);
      };
    }
  }
  if (window.Element && Element.prototype.animate) {
    const nativeAnimate = Element.prototype.animate;
    Element.prototype.animate = function (...a) {
      const animation = nativeAnimate.apply(this, a);
      if (!playing) pauseAnimation(animation);
      return animation;
    };
  }

  // ---------- SMIL ----------
  const svgProto = window.SVGSVGElement && window.SVGSVGElement.prototype;
  const nativePauseSVG = svgProto && svgProto.pauseAnimations;
  const nativeResumeSVG = svgProto && svgProto.unpauseAnimations;
  function pauseSVG(svg) {
    if (nativePauseSVG && !svg.animationsPaused()) {
      nativePauseSVG.call(svg);
      pausedSVGs.add(svg);
    }
  }
  if (nativePauseSVG && nativeResumeSVG) {
    svgProto.pauseAnimations = function () { pausedSVGs.delete(this); return nativePauseSVG.call(this); };
    svgProto.unpauseAnimations = function () {
      const r = nativeResumeSVG.call(this);
      if (!playing) pauseSVG(this);
      return r;
    };
  }

  function pauseDocumentAnimations() {
    if (document.getAnimations) for (const a of document.getAnimations()) pauseAnimation(a);
    for (const svg of document.querySelectorAll('svg')) pauseSVG(svg);
  }

  const observer = new MutationObserver(records => {
    if (playing) return;
    for (const record of records) {
      for (const node of record.addedNodes) {
        if (node.nodeType !== 1 || !node.isConnected) continue;
        if (svgProto && node instanceof SVGSVGElement) pauseSVG(node);
        for (const svg of node.querySelectorAll('svg')) pauseSVG(svg);
      }
    }
  });
  const observePaused = () => observer.observe(document, { childList: true, subtree: true });

  function applyPlayback() {
    const next = playing;
    style.disabled = next;
    if (next) {
      observer.disconnect();
      for (const a of pausedAnimations) {
        if (a.playState === 'paused' || a.pending) { try { nativePlay.call(a); } catch (_) {} }
      }
      pausedAnimations.clear();
      for (const svg of pausedSVGs) if (svg.isConnected) nativeResumeSVG.call(svg);
      pausedSVGs.clear();
      for (const [id, f] of frames) scheduleFrame(id, f);
      for (const [id, t] of timers) scheduleTimer(id, t);
    } else {
      for (const f of frames.values()) {
        if (f.native !== null) nativeCancelRAF(f.native);
        f.native = null;
      }
      for (const t of timers.values()) {
        if (t.native !== null) {
          nativeClearTimeout(t.native);
          t.remaining = Math.max(0, t.remaining - (virtualNow - t.started));
          t.native = null;
        }
      }
      pauseDocumentAnimations();
      observePaused();
    }
  }

  observePaused();
  document.addEventListener('DOMContentLoaded', () => { if (!playing) pauseDocumentAnimations(); });

  // Harness API. Absolute seek for SMIL / Web Animations, stepped advance for
  // rAF / timer driven artwork.
  function seek(target) {
    if (playing) { playing = false; applyPlayback(); }
    virtualNow = target;
    for (const svg of document.querySelectorAll('svg')) {
      if (svg.setCurrentTime) { try { svg.setCurrentTime(target / 1000); } catch (_) {} }
    }
    if (document.getAnimations) {
      for (const a of document.getAnimations()) {
        try {
          if (a.playState === 'idle') continue;
          a.pause();
          a.currentTime = target;
        } catch (_) {}
      }
    }
  }

  // Step the virtual clock forward by `ms` in 16ms slices. Callbacks are drained
  // from a microtask queue, so seeking a long way costs no wall-clock time.
  // Animation timestamps jump by the slice, which is what the frame sampler
  // compares; intermediate positions inside a slice are never samples.
  async function advance(ms) {
    const deadline = virtualNow + ms;
    // Timers already registered must catch fire during the step too, so swap
    // their native handles for queue entries before starting.
    playing = true;
    const queue = [];
    pumpQueue = queue;
    applyPlayback();
    try {
      while (virtualNow < deadline) {
        virtualNow += Math.min(16, deadline - virtualNow);
        let guard = 0;
        while (queue.length) {
          const batch = queue.splice(0, queue.length);
          for (const run of batch) {
            try { run(); } catch (_) { /* artwork errors must not stop the clock */ }
          }
          if (++guard > 64) break;
        }
        await Promise.resolve();
      }
    } finally {
      // Stop feeding the queue before tearing playback down, otherwise the
      // teardown itself is what re-arms artwork callbacks.
      pumpQueue = null;
      playing = false;
      applyPlayback();
    }
  }

  Object.defineProperty(window, '__plateauClock', {
    value: Object.freeze({
      seek,
      advance,
      now: () => virtualNow,
      setPlaying: v => { playing = v; applyPlayback(); },
      snapshot: () => ({ now: virtualNow, playing, frames: frames.size, timers: timers.size, animations: pausedAnimations.size }),
    }),
    writable: false, configurable: false, enumerable: false,
  });
})();
