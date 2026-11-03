<template>
  <!-- vue-router 5 no longer supports nesting <RouterView> inside <Transition>
       (VUE_ROUTER_R0060). Doing it anyway left every page with the same vnode type, so
       `mode="out-in"` never engaged: leaving and entering pages ran at once, stale roots
       piled up in the panel, and a page caught mid-enter stayed at `.page-enter-from`
       (opacity: 0) with nothing left to reveal it. Wrap the slot's resolved component
       instead; the key keeps query/filter changes from remounting the page. -->
  <RouterView v-slot="{ Component, route: current }">
    <Transition name="page" mode="out-in" :duration="PAGE_TRANSITION_MS">
      <component :is="Component" :key="consolePageKey(current)" />
    </Transition>
  </RouterView>
</template>

<script setup lang="ts">
import { RouterView } from 'vue-router'
import { consolePageKey } from './consoleShell'

/**
 * The CSS runs 180ms (opacity) / 220ms (transform); an explicit duration makes Vue end
 * the leave on a timer as well, so a page can never stay in the out-in placeholder when
 * the browser drops the transitionend event (hidden tab, element detached mid-flight).
 *
 * Readers who asked for reduced motion get the stylesheet's `transition: none`, so they
 * also skip the timer and swap pages immediately instead of waiting out the fade.
 */
const PREFERS_REDUCED_MOTION =
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
    : false
const PAGE_TRANSITION_MS = PREFERS_REDUCED_MOTION ? 0 : { enter: 220, leave: 220 }
</script>
