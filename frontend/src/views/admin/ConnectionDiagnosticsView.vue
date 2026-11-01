<template><AppLayout><div class="space-y-5"><AdminPageHeader :title="title" /><div class="flex flex-wrap gap-3"><input v-model="start" class="input w-auto" type="datetime-local" /><input v-model="end" class="input w-auto" type="datetime-local" /><button class="btn btn-primary" @click="apply">{{ refreshText }}</button></div><UsageConnections :filters="filters" :refresh-key="refreshKey" /></div></AppLayout></template>
<script setup lang="ts">
import {computed,ref} from 'vue'
import {useI18n} from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import UsageConnections from '@/components/admin/usage/UsageConnections.vue'
const {locale}=useI18n()
const title=computed(()=>locale.value==='zh'?'连接诊断':'Connection diagnostics')
const refreshText=computed(()=>locale.value==='zh'?'查询':'Search')
const local=(d:Date)=>new Date(d.getTime()-d.getTimezoneOffset()*60000).toISOString().slice(0,16)
const start=ref(local(new Date(Date.now()-3600000))),end=ref(local(new Date())),refreshKey=ref(0)
const filters=ref({start_time:new Date(start.value).toISOString(),end_time:new Date(end.value).toISOString()})
function apply(){const a=new Date(start.value),b=new Date(end.value);if(!Number.isFinite(a.getTime())||!Number.isFinite(b.getTime())||a>=b)return;filters.value={start_time:a.toISOString(),end_time:b.toISOString()};refreshKey.value++}
</script>
