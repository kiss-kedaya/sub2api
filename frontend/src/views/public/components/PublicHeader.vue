<template>
  <header class="sticky top-0 z-40 h-16 border-b border-default bg-default">
    <UContainer class="flex h-full items-center justify-between gap-3">
      <div class="flex min-w-0 items-center gap-1.5 lg:flex-1">
        <slot name="leading" />
        <slot name="brand">
          <RouterLink
            v-if="brandTo"
            :to="brandTo"
            class="-ms-1 flex min-w-0 items-center gap-2.5 rounded-md p-1 outline-primary/25 focus-visible:outline-3"
          >
            <img :src="logo || '/logo.svg'" alt="" class="size-8 shrink-0 rounded-md object-contain" />
            <span class="truncate text-base font-semibold text-highlighted">{{ siteName }}</span>
          </RouterLink>
          <span v-else class="flex min-w-0 items-center gap-2.5">
            <img :src="logo || '/logo.svg'" alt="" class="size-8 shrink-0 rounded-md object-contain" />
            <span class="truncate text-base font-semibold text-highlighted">{{ siteName }}</span>
          </span>
        </slot>
        <template v-if="section">
          <span class="hidden text-dimmed sm:inline" aria-hidden="true">/</span>
          <span class="hidden truncate text-sm font-medium text-muted sm:inline">{{ section }}</span>
        </template>
      </div>

      <div v-if="$slots.default" class="hidden min-w-0 lg:flex">
        <slot />
      </div>

      <div class="flex shrink-0 items-center justify-end gap-1 lg:flex-1">
        <slot name="actions" />
      </div>
    </UContainer>
  </header>
</template>

<script setup lang="ts">
// Top bar of the public pages (home, usage lookup, docs, legal, model plaza), laid out like
// Nuxt UI's UHeader: brand left, optional centre links, actions right. UHeader itself is not
// used because it needs the router at mount, which several public-page specs do not install.
withDefaults(defineProps<{
  siteName: string
  logo?: string
  /** Where the brand links to; omit to render it as plain text (the home page itself). */
  brandTo?: string
  /** Section name shown after the brand, e.g. the docs title. */
  section?: string
}>(), {
  logo: '',
  brandTo: undefined,
  section: ''
})
</script>
