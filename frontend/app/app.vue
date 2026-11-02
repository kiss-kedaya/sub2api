<template>
  <AppShell><NuxtPage :page-key="pageKey" /></AppShell>
</template>

<script setup lang="ts">
import type { RouteLocationNormalized } from 'vue-router'
import AppShell from '@/App.vue'
import { consolePageKey } from '@/components/layout/consoleShell'

const pageKey = (route: RouteLocationNormalized) => consolePageKey(route)
</script>
