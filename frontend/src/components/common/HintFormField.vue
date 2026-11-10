<script setup lang="ts">
import HelpTooltip from '@/components/common/HelpTooltip.vue'

// $attrs are forwarded explicitly to UFormField below; do not also fall through onto the root.
defineOptions({ inheritAttrs: false })

// A form field whose explanatory copy lives in a help bubble next to the label instead of as permanent
// muted text under the field. It is a thin shell around UFormField: everything except `hint` / `hintTitle` /
// `keepHintVisible` is forwarded untouched, so class / required / error / ui / the default slot and any
// #label override behave exactly as before.
//
// Usage in the account modals (see the call sites, e.g. CreateAccountModal.vue):
//   <HintFormField :label="<label key>" :hint="<hint key>"> ...field... </HintFormField>
//   <HintFormField :label="..." :hint="..." keep-hint-visible>   <!-- one-liner that changes how you fill it in -->
withDefaults(defineProps<{
  label?: string
  hint?: string
  hintTitle?: string
  keepHintVisible?: boolean
  hintWidthClass?: string
}>(), {
  label: undefined,
  hint: undefined,
  hintTitle: undefined,
  keepHintVisible: false,
  hintWidthClass: 'w-64',
})
</script>

<template>
  <UFormField
    v-bind="$attrs"
    :label="undefined"
    :description="keepHintVisible ? hint : undefined"
  >
    <template #label>
      <!-- Keep a caller-provided label slot working unchanged. -->
      <slot name="label">
        <span class="inline-flex items-center">
          <span>{{ label }}</span>
          <HelpTooltip
            v-if="hint && !keepHintVisible"
            trigger="auto"
            :aria-label="label"
            :width-class="hintWidthClass"
          >
            <span v-if="hintTitle" class="mb-1 block font-medium text-highlighted">{{ hintTitle }}</span>
            <span class="block">{{ hint }}</span>
          </HelpTooltip>
        </span>
      </slot>
    </template>
    <slot />
  </UFormField>
</template>
