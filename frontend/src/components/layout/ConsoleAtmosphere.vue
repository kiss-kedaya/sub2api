<template>
  <div class="console-atmosphere" :class="{ 'motion-paused': visibility !== 'visible' }" aria-hidden="true">
    <div class="console-paper" />
    <div class="console-draft console-draft-primary"><i /><i /><i /></div>
    <div class="console-draft console-draft-secondary"><i /><i /><i /></div>
    <div class="console-trace"><i /></div>
  </div>
</template>

<script setup lang="ts">
import { useDocumentVisibility } from '@vueuse/core'

const visibility = useDocumentVisibility()
</script>

<style scoped>
.console-atmosphere {
  position: fixed;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  contain: strict;
  z-index: -1;
}

.console-paper {
  position: absolute;
  inset: 0;
  background-image: var(--signal-paper-image);
  background-repeat: repeat;
  background-size: 256px 256px;
  mix-blend-mode: var(--signal-paper-blend);
  opacity: var(--signal-paper-opacity);
}

.console-draft {
  position: absolute;
  width: 360px;
  height: 330px;
  border: 1px solid var(--signal-draft-ink);
  background-image: linear-gradient(var(--signal-draft-ink) 1px, transparent 1px), linear-gradient(90deg, var(--signal-draft-ink) 1px, transparent 1px);
  background-size: 60px 60px;
  opacity: var(--signal-draft-opacity);
}

.console-draft::before,
.console-draft::after {
  content: '';
  position: absolute;
  background: var(--signal-draft-line);
}

.console-draft::before { top: 50%; left: -64px; right: -64px; height: 1px; }
.console-draft::after { left: 50%; top: -48px; bottom: -48px; width: 1px; }
.console-draft-primary { top: 76px; right: -48px; transform: rotate(-12deg); }
.console-draft-secondary { left: calc(var(--signal-nav-size, 0px) - 230px); bottom: -210px; transform: rotate(14deg); }
.console-draft i { position: absolute; width: 5px; height: 5px; background: #009bd0; opacity: 0.5; }
.console-draft i:nth-child(1) { top: -3px; left: -3px; }
.console-draft i:nth-child(2) { bottom: -3px; right: -3px; }
.console-draft i:nth-child(3) { top: calc(50% - 2px); left: calc(50% - 2px); }

.console-trace { position: absolute; top: 57%; right: 0; width: 180px; border-top: 1px solid var(--signal-draft-ink); transform: rotate(-24deg); }
.console-trace i { display: block; width: 5px; height: 5px; margin-top: -3px; background: #009bd0; opacity: 0.55; }

@media (prefers-reduced-motion: no-preference) {
  .console-trace i { animation: console-trace-travel 12s ease-in-out infinite; }
  .motion-paused .console-trace i { animation-play-state: paused; }
}

@keyframes console-trace-travel {
  0%, 100% { transform: translateX(0); opacity: 0; }
  15%, 75% { opacity: 0.55; }
  90% { transform: translateX(170px); opacity: 0; }
}

@media (max-width: 639px) {
  .console-draft { width: 240px; height: 220px; background-size: 55px 55px; }
  .console-draft-primary { right: -72px; top: 76px; }
  .console-draft-secondary,
  .console-trace { display: none; }
}
</style>
