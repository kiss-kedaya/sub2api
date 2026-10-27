<template><AppLayout><TrafficPanel :admin="admin" /></AppLayout></template>
<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import TrafficPanel from '@/components/usage/TrafficPanel.vue'
withDefaults(defineProps<{admin?:boolean}>(),{admin:false})
</script>
