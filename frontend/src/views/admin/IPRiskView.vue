<template><AppLayout><IPRiskSettings expanded /></AppLayout></template>
<script setup lang="ts">
// On its own page the settings open right away (the collapsed card only suits an embedded section).
import AppLayout from '@/components/layout/AppLayout.vue'
import IPRiskSettings from '@/features/ip-risk/IPRiskSettings.vue'
</script>
