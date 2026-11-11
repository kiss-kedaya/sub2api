<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, useSlots, watch } from 'vue'
import { useSettingPanel } from '@/components/common/settingPanel'

// One compact line of a settings list: caret + name + inline summary + the control on the right.
// Clicking the line (or Enter / Space on it) reveals the description and, when the setting is on,
// the fields that setting exposes. Clicking the control does NOT toggle the row: the control is a
// sibling of the row header, not a child of it, so there is no event to stop.
//
// This component owns its open state on purpose. Callers only say whether the setting is on; they
// do not have to keep a per-row `open` ref for every row of a long form.
//
// Reusable outside the account dialogs: nothing here knows what the rows mean.
const props = withDefaults(defineProps<{
  /** Already-translated name of the setting. */
  label: string
  /** Already-translated explanation, shown when the row is expanded. Pass several for several paragraphs. */
  hint?: string | string[]
  /** Already-translated current value, shown inline when the setting is on. */
  summary?: string
  /** Whether the setting is on. Drives the highlight colour and the auto-expand. */
  on?: boolean
  /**
   * Show the summary even while the row is off. For rows that are a value rather than an on/off
   * switch (a list of capabilities, say), where "off" would otherwise hide what is configured.
   */
  persistentSummary?: boolean
  /** Start expanded. Mostly for tests and for rows that are the whole point of a section. */
  defaultOpen?: boolean
  /** Optional test id on the row root. */
  testId?: string
  /**
   * Whether this row may appear in the panel's "enabled" summary. Turn off for rows that are a
   * plain value (a threshold, a mode) rather than a feature someone switched on.
   */
  inSummary?: boolean
  /**
   * Give the control half of the row instead of its intrinsic width. For text and URL inputs,
   * whose content is the thing to read; a switch or a short number keeps the default.
   */
  wideControl?: boolean
}>(), {
  wideControl: false,
  inSummary: true,
  hint: undefined,
  summary: undefined,
  on: false,
  persistentSummary: false,
  defaultOpen: false,
  testId: undefined,
})

const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

const slots = useSlots()
const uid = useId()
const bodyId = computed(() => `setting-row-body-${uid}`)

const hints = computed(() => (Array.isArray(props.hint) ? props.hint : [props.hint]).filter((h): h is string => Boolean(h)))
const hasBody = computed(() => hints.value.length > 0 || Boolean(slots.hint) || Boolean(slots.fields))
const hasFields = computed(() => Boolean(slots.fields))

const open = ref(props.defaultOpen)

// Turning a setting on is the moment the user wants to fill its fields in, so open the row for
// them. Only a change the user just made counts: a form that loads its values after mounting also
// flips `on` from false to true, and expanding every row that is on at load is what made the old
// page unreadable. The control slot records when it was last pressed; `on` turning true within a
// short window after that is the user's doing.
const USER_CHANGE_WINDOW_MS = 1000
let lastControlInteraction = Number.NEGATIVE_INFINITY
function markControlInteraction() {
  lastControlInteraction = performance.now()
}
watch(() => props.on, (now, before) => {
  if (!now || before || !hasFields.value) return
  if (performance.now() - lastControlInteraction > USER_CHANGE_WINDOW_MS) return
  open.value = true
})

// Report to the enclosing SettingPanel (if any) so its summary bar lists this row while it is on.
const root = ref<HTMLElement | null>(null)
const panel = useSettingPanel()
panel?.register({ id: uid, label: () => props.label, on: () => props.inSummary && props.on, el: root })
onBeforeUnmount(() => panel?.unregister(uid))

function toggle() {
  if (!hasBody.value) return
  open.value = !open.value
  emit('update:open', open.value)
}
</script>

<template>
  <div ref="root" class="setting-row" :data-on="on || undefined" :data-wide-control="wideControl || undefined" :data-testid="testId">
    <div class="setting-row__head">
      <!-- Before the name: a bulk form's "change this field" checkbox. Pressing it counts as using
           the control, so ticking a row opens it on the value to fill in. -->
      <div
        v-if="$slots.leading"
        class="setting-row__leading"
        @click.capture="markControlInteraction"
        @keydown.capture="markControlInteraction"
      >
        <slot name="leading" />
      </div>
      <button
        type="button"
        class="setting-row__button"
        :aria-expanded="hasBody ? open : undefined"
        :aria-controls="hasBody ? bodyId : undefined"
        :disabled="hasBody ? undefined : true"
        @click="toggle"
      >
        <span class="setting-row__caret" :data-static="hasBody ? undefined : true" aria-hidden="true" />
        <span class="setting-row__label" :title="label"><slot name="title"><span class="setting-row__label-text">{{ label }}</span></slot></span>
        <!-- An off row has nothing to report: its name alone says it is at the default. -->
        <span v-if="summary && (on || persistentSummary)" class="setting-row__summary" :title="summary">{{ summary }}</span>
      </button>
      <div
        v-if="$slots.control"
        class="setting-row__control"
        @click.capture="markControlInteraction"
        @keydown.capture="markControlInteraction"
      >
        <slot name="control" />
      </div>
    </div>

    <!-- Warnings and live state: always visible, never folded into the collapsible body. -->
    <div v-if="$slots.status" class="setting-row__status">
      <slot name="status" />
    </div>

    <div :id="bodyId" class="setting-row__body" :data-open="open">
      <!-- inert, not v-show: a closed row keeps its height animated but nothing inside can be focused. -->
      <div class="setting-row__body-inner" :inert="open ? undefined : true">
        <p v-for="(text, index) in hints" :key="index" class="setting-row__hint">{{ text }}</p>
        <!-- For an explanation that is more than plain text (several spans, a live value, an id for aria). -->
        <div v-if="$slots.hint" class="setting-row__hint"><slot name="hint" /></div>
        <slot name="fields" />
      </div>
    </div>
  </div>
</template>

<style scoped>
/* A row is a grid item of the group above it, and grid items default to min-width: auto, which
   refuses to shrink below the min-content of a nowrap child. Without this, one long setting name or
   one long value pushes the whole panel wider than the screen. */
.setting-row {
  min-width: 0;
  border-top: 1px solid var(--ui-border);
}

.setting-row__head {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  min-height: 40px;
}

.setting-row__button {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 10px;
  min-width: 0;
  min-height: 40px;
  padding: 0;
  text-align: start;
  cursor: pointer;
}

/* A row with nothing to reveal still keeps the caret's space so the names stay on one axis. */
.setting-row__button:disabled {
  cursor: default;
}

.setting-row__caret {
  flex: none;
  width: 6px;
  height: 6px;
  margin-inline-start: 3px;
  border-inline-end: 1.5px solid var(--ui-text-dimmed);
  border-block-end: 1.5px solid var(--ui-text-dimmed);
  rotate: -45deg;
  transition: rotate 150ms var(--cb-ease-out);
}

.setting-row__button[aria-expanded='true'] > .setting-row__caret {
  rotate: 45deg;
}

.setting-row__caret[data-static] {
  visibility: hidden;
}

/* The name never wraps. It keeps its full width while there is room and only ellipsizes once the
   summary has already given up all of its space, so the control on the right is never pushed out. */
.setting-row__label {
  display: inline-flex;
  flex: 0 1 auto;
  align-items: center;
  gap: 8px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  font-weight: 500;
  line-height: 20px;
  color: var(--ui-text-highlighted);
  white-space: nowrap;
}

/* The ellipsis has to live on the element whose text is actually cut: the label is a flex box (so a
   #title slot can put a badge beside the name), and text-overflow does nothing on a flex container. */
.setting-row__label-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Off rows step back: the name is still readable, it just is not what is happening. */
/* Child combinators throughout: a row can sit inside another row's body (a provider's fields under
   its switch), and the outer row's state must not leak into the inner one. */
.setting-row:not([data-on]) > .setting-row__head .setting-row__label {
  color: var(--ui-text-toned);
}

.setting-row__summary {
  min-width: 0;
  flex: 1 1 0;
  overflow: hidden;
  font-size: 12px;
  line-height: 18px;
  color: var(--ui-text-muted);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The control gives way before the name does: on a phone a wide text input would otherwise squeeze
   the name down to one letter and an ellipsis. The name keeps at least ~9rem (or 45% of the row). */
.setting-row__control {
  display: inline-flex;
  flex: 0 1 auto;
  align-items: center;
  min-width: 0;
}

.setting-row__button {
  min-width: min(45%, 9rem);
}

/* A wide control takes half the row on a desktop, so a URL can be read without opening the row.
   The name gives up its growth instead. */
@media (min-width: 640px) {
  .setting-row[data-wide-control] > .setting-row__head > .setting-row__button {
    flex: 0 1 auto;
  }

  .setting-row[data-wide-control] > .setting-row__head > .setting-row__control {
    flex: 1 1 50%;
    justify-content: flex-end;
    max-width: 28rem;
    margin-inline-start: auto;
  }
}

/* On a phone there is no room for a name and a text input side by side: sharing one line cut the
   name to "User Message…" and the value to "https://<account_id". The input drops to its own line
   under the name, full width, so both can be read. Switches and short numbers stay beside the name. */
@media (max-width: 639px) {
  .setting-row[data-wide-control] > .setting-row__head {
    flex-wrap: wrap;
    row-gap: 0;
  }

  .setting-row[data-wide-control] > .setting-row__head > .setting-row__button {
    flex: 1 1 100%;
  }

  .setting-row[data-wide-control] > .setting-row__head > .setting-row__control {
    flex: 1 1 100%;
    /* Line up with the name, past the caret. */
    padding-inline-start: 19px;
    padding-block-end: 8px;
  }

  /* The name has the whole line now, so it may wrap instead of ending in an ellipsis. */
  .setting-row[data-wide-control] > .setting-row__head .setting-row__label,
  .setting-row[data-wide-control] > .setting-row__head .setting-row__label-text {
    white-space: normal;
    overflow: visible;
  }

  /* Every other row keeps its switch or short number beside the name, but on a phone the name
     wraps onto a second line rather than being cut to "Enable Stream Timeout Handli…": a setting
     whose name cannot be read cannot be set. The row grows; the control stays centred on it. */
  .setting-row:not([data-wide-control]) > .setting-row__head .setting-row__label {
    overflow: visible;
    white-space: normal;
    text-wrap: pretty;
  }

  .setting-row:not([data-wide-control]) > .setting-row__head .setting-row__label-text {
    overflow: visible;
  }

  .setting-row:not([data-wide-control]) > .setting-row__head > .setting-row__button {
    padding-block: 10px;
  }

  /* A summary squeezed beside the name ends up as one letter ("O" for "Off"). On a phone it takes
     the line under the name instead, aligned with it past the caret. */
  .setting-row:not([data-wide-control]) > .setting-row__head > .setting-row__button {
    flex-wrap: wrap;
    row-gap: 0;
  }

  .setting-row:not([data-wide-control]) > .setting-row__head .setting-row__label {
    flex: 1 1 0;
  }

  .setting-row:not([data-wide-control]) > .setting-row__head .setting-row__summary {
    flex: 1 1 100%;
    padding-inline-start: 19px;
  }
}

.setting-row__leading {
  display: inline-flex;
  flex: none;
  align-items: center;
}

/* A row with a leading checkbox indents its body past the checkbox too, so the hint lines up with the name. */
.setting-row:has(> .setting-row__head > .setting-row__leading) > .setting-row__body > .setting-row__body-inner,
.setting-row:has(> .setting-row__head > .setting-row__leading) > .setting-row__status {
  padding-inline-start: 44px;
}

.setting-row__status {
  display: grid;
  min-width: 0;
  padding-inline-start: 16px;
}

/* Slot content that rendered nothing (a v-if that is false leaves only a comment) takes no room. */
.setting-row__status:empty {
  display: none;
}

.setting-row__body {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 180ms var(--cb-ease-out);
}

.setting-row__body[data-open='true'] {
  grid-template-rows: 1fr;
}

.setting-row__body-inner {
  display: grid;
  gap: 10px;
  min-height: 0;
  overflow: hidden;
  padding-inline-start: 16px;
}

.setting-row__body[data-open='true'] > .setting-row__body-inner {
  padding-block-end: 12px;
}

.setting-row__hint {
  max-width: 64ch;
  font-size: 13px;
  line-height: 20px;
  color: var(--ui-text-muted);
  text-wrap: pretty;
}

@media (prefers-reduced-motion: reduce) {
  .setting-row__caret,
  .setting-row__body {
    transition: none;
  }
}
</style>
