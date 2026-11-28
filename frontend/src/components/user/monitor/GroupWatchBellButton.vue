<template>
  <button
    type="button"
    class="group-watch-bell"
    :class="[
      platformToneClass,
      {
        'is-subscribed': subscribed,
        'is-pending': pending,
        'is-disabled': disabled && !pending,
        'is-ringing': motionPhase === 'ringing',
        'is-muting': motionPhase === 'muting',
      },
    ]"
    :disabled="pending || disabled"
    :aria-pressed="subscribed"
    :aria-label="buttonLabel"
    :title="tooltipText"
    data-testid="group-watch-bell"
    @click="handleClick"
  >
    <span class="group-watch-bell__ripple" aria-hidden="true" />
    <svg
      class="group-watch-bell__svg"
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
    >
      <!-- Acoustic ring waves: appear softly when subscribed -->
      <g class="group-watch-bell__waves">
        <path
          d="M5.15 6.4A9.2 9.2 0 0 0 3.3 11.1"
          stroke="currentColor"
          stroke-width="1.65"
          stroke-linecap="round"
        />
        <path
          d="M18.85 6.4A9.2 9.2 0 0 1 20.7 11.1"
          stroke="currentColor"
          stroke-width="1.65"
          stroke-linecap="round"
        />
      </g>

      <!-- Bell dome with separate pivot so the swing originates naturally from the top loop -->
      <g class="group-watch-bell__dome">
        <path
          d="M12 3.1v1.1"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
        />
        <path
          class="group-watch-bell__body"
          d="M17.4 10.6a5.4 5.4 0 1 0-10.8 0c0 3.55-1.08 5.2-1.85 6.08a.85.85 0 0 0 .64 1.42h13.22a.85.85 0 0 0 .64-1.42c-.77-.88-1.85-2.53-1.85-6.08Z"
          stroke="currentColor"
          stroke-width="1.75"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </g>

      <!-- Clapper swings slightly out of phase for realistic physical settle -->
      <g class="group-watch-bell__clapper">
        <path
          d="M9.85 18.2a2.25 2.25 0 0 0 4.3 0"
          stroke="currentColor"
          stroke-width="1.85"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </g>
    </svg>
  </button>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  platform: string
  subscribed?: boolean
  pending?: boolean
  disabled?: boolean
  groupName?: string
}>(), {
  subscribed: false,
  pending: false,
  disabled: false,
  groupName: '',
})

const emit = defineEmits<{
  toggle: []
}>()

const { t } = useI18n()
const motionPhase = ref<'idle' | 'ringing' | 'muting'>('idle')
let motionTimer: number | null = null

const KNOWN_PLATFORMS = new Set([
  'anthropic',
  'openai',
  'gemini',
  'grok',
  'antigravity',
  'kimi',
  'zhipu',
  'deepseek',
  'minimax',
])

const platformToneClass = computed(() => {
  const normalized = (props.platform || '').trim().toLowerCase()
  return KNOWN_PLATFORMS.has(normalized) ? `is-tone-${normalized}` : 'is-tone-default'
})

const buttonLabel = computed(() => {
  const name = props.groupName || t('channelMonitorV3.unknownGroup')
  return props.subscribed
    ? t('channelMonitorV3.watchUnsubscribeAria', { name })
    : t('channelMonitorV3.watchSubscribeAria', { name })
})

const tooltipText = computed(() => (
  props.subscribed
    ? t('channelMonitorV3.watchSubscribedTooltip')
    : t('channelMonitorV3.watchSubscribeTooltip')
))

function triggerMotion(phase: 'ringing' | 'muting', durationMs: number) {
  if (motionTimer !== null) {
    window.clearTimeout(motionTimer)
    motionTimer = null
  }
  motionPhase.value = 'idle'
  // Force a microtask/frame reset only when needed so rapid clicks replay cleanly.
  motionPhase.value = phase
  motionTimer = window.setTimeout(() => {
    motionPhase.value = 'idle'
    motionTimer = null
  }, durationMs)
}

watch(
  () => props.subscribed,
  (next, prev) => {
    if (next === prev) return
    if (next) {
      triggerMotion('ringing', 560)
    } else {
      triggerMotion('muting', 200)
    }
  },
)

function handleClick() {
  if (props.pending || props.disabled) return
  emit('toggle')
}

onBeforeUnmount(() => {
  if (motionTimer !== null) {
    window.clearTimeout(motionTimer)
    motionTimer = null
  }
})
</script>

<style scoped>
.group-watch-bell {
  --bell-idle: rgb(148 163 184);
  --bell-idle-hover: rgb(71 85 105);
  --bell-idle-bg-hover: rgb(15 23 42 / 0.055);
  --bell-accent: var(--ui-primary, #155dfc);
  --bell-accent-bg: color-mix(in srgb, var(--bell-accent) 12%, transparent);
  --bell-accent-ring: color-mix(in srgb, var(--bell-accent) 30%, transparent);
  --bell-accent-glow: color-mix(in srgb, var(--bell-accent) 36%, transparent);

  position: relative;
  display: inline-grid;
  place-items: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 9px;
  background: transparent;
  color: var(--bell-idle);
  cursor: pointer;
  appearance: none;
  isolation: isolate;
  transition:
    transform 140ms cubic-bezier(0.23, 1, 0.32, 1),
    color 180ms cubic-bezier(0.23, 1, 0.32, 1),
    background-color 180ms cubic-bezier(0.23, 1, 0.32, 1),
    border-color 180ms cubic-bezier(0.23, 1, 0.32, 1),
    box-shadow 180ms cubic-bezier(0.23, 1, 0.32, 1);
}

html.dark .group-watch-bell {
  --bell-idle: rgb(161 161 170);
  --bell-idle-hover: rgb(244 244 245);
  --bell-idle-bg-hover: rgb(255 255 255 / 0.08);
}

/* Platform-matched active bell tones (aligned with providerBadgeClass & providerGradient) */
.group-watch-bell.is-tone-anthropic {
  --bell-accent: #ea580c;
}
html.dark .group-watch-bell.is-tone-anthropic {
  --bell-accent: #fb923c;
}

.group-watch-bell.is-tone-openai {
  --bell-accent: #059669;
}
html.dark .group-watch-bell.is-tone-openai {
  --bell-accent: #34d399;
}

.group-watch-bell.is-tone-gemini {
  --bell-accent: #0284c7;
}
html.dark .group-watch-bell.is-tone-gemini {
  --bell-accent: #38bdf8;
}

.group-watch-bell.is-tone-grok {
  --bell-accent: #27272a;
}
html.dark .group-watch-bell.is-tone-grok {
  --bell-accent: #e4e4e7;
}

.group-watch-bell.is-tone-antigravity {
  --bell-accent: #9333ea;
}
html.dark .group-watch-bell.is-tone-antigravity {
  --bell-accent: #c084fc;
}

.group-watch-bell.is-tone-kimi {
  --bell-accent: #db2777;
}
html.dark .group-watch-bell.is-tone-kimi {
  --bell-accent: #f472b6;
}

.group-watch-bell.is-tone-zhipu {
  --bell-accent: #4f46e5;
}
html.dark .group-watch-bell.is-tone-zhipu {
  --bell-accent: #818cf8;
}

.group-watch-bell.is-tone-deepseek {
  --bell-accent: #0d9488;
}
html.dark .group-watch-bell.is-tone-deepseek {
  --bell-accent: #2dd4bf;
}

.group-watch-bell.is-tone-minimax {
  --bell-accent: #e11d48;
}
html.dark .group-watch-bell.is-tone-minimax {
  --bell-accent: #fb7185;
}

@media (hover: hover) and (pointer: fine) {
  .group-watch-bell:not(.is-subscribed):not(:disabled):hover {
    color: var(--bell-idle-hover);
    background: var(--bell-idle-bg-hover);
  }

  .group-watch-bell.is-subscribed:not(:disabled):hover {
    background: color-mix(in srgb, var(--bell-accent) 18%, transparent);
    border-color: color-mix(in srgb, var(--bell-accent) 40%, transparent);
  }
}

.group-watch-bell:not(:disabled):active {
  transform: scale(0.91);
}

.group-watch-bell:focus-visible {
  outline: 2px solid var(--bell-accent);
  outline-offset: 2px;
}

.group-watch-bell.is-subscribed {
  color: var(--bell-accent);
  background: var(--bell-accent-bg);
  border-color: var(--bell-accent-ring);
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 0.35),
    0 2px 8px -2px var(--bell-accent-glow);
}

html.dark .group-watch-bell.is-subscribed {
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 0.12),
    0 2px 10px -2px color-mix(in srgb, var(--bell-accent) 28%, transparent);
}

.group-watch-bell.is-pending {
  opacity: 0.7;
  cursor: progress;
}

.group-watch-bell.is-disabled {
  opacity: 0.42;
  cursor: not-allowed;
}

.group-watch-bell__svg {
  width: 18px;
  height: 18px;
  overflow: visible;
  pointer-events: none;
}

.group-watch-bell__body {
  fill: currentColor;
  fill-opacity: 0;
  transition: fill-opacity 200ms cubic-bezier(0.23, 1, 0.32, 1);
}

.group-watch-bell.is-subscribed .group-watch-bell__body {
  fill-opacity: 0.22;
}

.group-watch-bell__dome {
  transform-box: view-box;
  transform-origin: 12px 4px;
}

.group-watch-bell__clapper {
  transform-box: view-box;
  transform-origin: 12px 5px;
}

.group-watch-bell__waves {
  opacity: 0;
  transform-box: fill-box;
  transform-origin: center;
  transform: scale(0.76);
  transition:
    opacity 180ms cubic-bezier(0.23, 1, 0.32, 1),
    transform 220ms cubic-bezier(0.23, 1, 0.32, 1);
}

.group-watch-bell.is-subscribed .group-watch-bell__waves {
  opacity: 0.88;
  transform: scale(1);
}

.group-watch-bell__ripple {
  position: absolute;
  inset: 0;
  border-radius: inherit;
  border: 1.5px solid var(--bell-accent);
  opacity: 0;
  pointer-events: none;
  transform: scale(0.8);
}

.group-watch-bell.is-ringing {
  animation: watch-bell-pop 340ms cubic-bezier(0.23, 1, 0.32, 1);
}

.group-watch-bell.is-ringing .group-watch-bell__dome {
  animation: watch-bell-swing 540ms cubic-bezier(0.22, 1, 0.36, 1);
}

.group-watch-bell.is-ringing .group-watch-bell__clapper {
  animation: watch-bell-clapper 540ms cubic-bezier(0.22, 1, 0.36, 1);
}

.group-watch-bell.is-ringing .group-watch-bell__waves {
  animation: watch-bell-waves 420ms cubic-bezier(0.23, 1, 0.32, 1);
}

.group-watch-bell.is-ringing .group-watch-bell__ripple {
  animation: watch-bell-ripple 480ms cubic-bezier(0.23, 1, 0.32, 1);
}

.group-watch-bell.is-muting {
  animation: watch-bell-mute 190ms cubic-bezier(0.23, 1, 0.32, 1);
}

@keyframes watch-bell-pop {
  0% { transform: scale(0.92); }
  42% { transform: scale(1.09); }
  74% { transform: scale(0.985); }
  100% { transform: scale(1); }
}

@keyframes watch-bell-swing {
  0% { transform: rotate(0deg); }
  18% { transform: rotate(16deg); }
  36% { transform: rotate(-13deg); }
  54% { transform: rotate(9deg); }
  72% { transform: rotate(-5deg); }
  86% { transform: rotate(2deg); }
  100% { transform: rotate(0deg); }
}

@keyframes watch-bell-clapper {
  0% { transform: rotate(0deg); }
  22% { transform: rotate(21deg); }
  42% { transform: rotate(-16deg); }
  60% { transform: rotate(11deg); }
  78% { transform: rotate(-6deg); }
  100% { transform: rotate(0deg); }
}

@keyframes watch-bell-waves {
  0% { opacity: 0; transform: scale(0.7); }
  45% { opacity: 1; transform: scale(1.08); }
  100% { opacity: 0.88; transform: scale(1); }
}

@keyframes watch-bell-ripple {
  0% { opacity: 0.56; transform: scale(0.76); }
  100% { opacity: 0; transform: scale(1.52); }
}

@keyframes watch-bell-mute {
  0% { transform: scale(0.93); }
  100% { transform: scale(1); }
}

@media (max-width: 639px) {
  .group-watch-bell {
    width: 36px;
    height: 36px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .group-watch-bell,
  .group-watch-bell__body,
  .group-watch-bell__waves {
    transition: color 120ms ease, background-color 120ms ease, border-color 120ms ease, opacity 120ms ease;
  }

  .group-watch-bell.is-ringing,
  .group-watch-bell.is-muting,
  .group-watch-bell.is-ringing .group-watch-bell__dome,
  .group-watch-bell.is-ringing .group-watch-bell__clapper,
  .group-watch-bell.is-ringing .group-watch-bell__waves,
  .group-watch-bell.is-ringing .group-watch-bell__ripple {
    animation: none;
  }

  .group-watch-bell:not(:disabled):active {
    transform: none;
  }
}
</style>
