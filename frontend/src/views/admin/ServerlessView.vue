<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-3"><div><h1 class="text-2xl font-semibold">{{ t('serverless.title') }}</h1><p class="mt-2 text-sm text-gray-500">{{ t('serverless.description') }}</p></div><button class="btn btn-secondary" :disabled="busy" @click="load">{{ t('serverless.refresh') }}</button></header>
      <p v-if="error" role="alert" class="rounded-xl bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ error }}</p><p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
      <template v-if="status && draft">
        <section class="card space-y-3 p-5"><p class="text-sm text-gray-500">{{ t('serverless.setup') }}</p><div class="flex flex-wrap gap-4 text-sm"><span v-for="field in runtimeFields" :key="field.key">{{ t(field.label) }}: {{ status.runtime[field.key] ? t('serverless.yes') : t('serverless.no') }}</span></div></section>
        <form class="space-y-5" @submit.prevent="save">
          <section class="card space-y-4 p-5"><label class="flex items-center gap-2 font-medium"><input v-model="draft.enabled" type="checkbox" :disabled="!status.runtime.secret_configured || !status.runtime.redis_configured" />{{ t('serverless.enabled') }}</label><p class="text-sm text-gray-500">{{ t('serverless.continuity') }}</p></section>
          <section class="card space-y-4 p-5"><div class="flex items-center justify-between"><h2 class="font-semibold">{{ t('serverless.nodes') }}</h2><button type="button" class="btn btn-secondary" @click="draft.pods.push({ id: '', endpoint: '', enabled: false })">{{ t('serverless.add') }}</button></div><div v-for="(pod, index) in draft.pods" :key="index" class="grid gap-3 rounded-xl border p-3 sm:grid-cols-[1fr_2fr_auto] dark:border-dark-700"><label class="text-sm">{{ t('serverless.nodeId') }}<input v-model.trim="pod.id" class="input mt-1 w-full" maxlength="63" required /></label><label class="text-sm">{{ t('serverless.endpoint') }}<input v-model.trim="pod.endpoint" class="input mt-1 w-full" type="url" maxlength="2048" required /></label><div class="flex flex-col justify-end gap-2"><label class="text-sm"><input v-model="pod.enabled" type="checkbox" /> {{ t('serverless.available') }}</label><button type="button" class="text-sm text-red-600" @click="removeNode(index)">{{ t('serverless.remove') }}</button></div></div></section>
          <section class="card space-y-4 p-5"><div class="flex items-center justify-between"><h2 class="font-semibold">{{ t('serverless.regions') }}</h2><button type="button" class="btn btn-secondary" @click="draft.regions.push({ country: '', pod_ids: [] })">{{ t('serverless.add') }}</button></div><p class="text-sm text-gray-500">{{ t('serverless.countriesHint') }}</p><div v-for="(region, index) in draft.regions" :key="index" class="flex flex-wrap items-end gap-3"><label class="text-sm">{{ t('serverless.country') }}<input  :value="region.country" @input="region.country = ($event.target as HTMLInputElement).value.toUpperCase().trim()" class="input mt-1 w-24 uppercase" maxlength="2" pattern="[A-Z]{2}" required /></label><fieldset class="min-w-48 flex-1 text-sm"><legend class="mb-2">{{ t('serverless.ids') }}</legend><div class="flex flex-wrap gap-2"><label v-for="id in nodeChoices(region.pod_ids)" :key="id" class="flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-600"><input type="checkbox" :checked="region.pod_ids.includes(id)" @change="toggleNode(region, id, ($event.target as HTMLInputElement).checked)" /><span>{{ id }}</span></label><span v-if="!nodeChoices(region.pod_ids).length" class="text-gray-500">{{ t('serverless.addNodesFirst') }}</span></div></fieldset><button type="button" class="btn btn-secondary" @click="draft.regions.splice(index, 1)">{{ t('serverless.remove') }}</button></div></section>
          <div class="flex justify-end"><button class="btn btn-primary" :disabled="busy">{{ t('serverless.save') }}</button></div>
        </form>
        <section class="card p-5"><h2 class="mb-3 font-semibold">{{ t('serverless.heartbeat') }}</h2><p v-if="!status.pods.length" class="text-sm text-gray-500">{{ t('serverless.empty') }}</p><div v-for="pod in status.pods" :key="pod.id" class="flex flex-wrap justify-between gap-2 border-t py-3 text-sm dark:border-dark-700"><span>{{ pod.id }} · {{ pod.region || '—' }}</span><span>{{ t('serverless.ready') }}: {{ pod.ready ? t('serverless.yes') : t('serverless.no') }}</span><span>{{ t('serverless.version') }}: {{ pod.version }}</span></div></section>
        <details class="card p-5"><summary class="cursor-pointer font-semibold">{{ t('serverless.advanced') }}</summary><div class="mt-3 flex gap-8 text-sm"><span>{{ t('serverless.requests') }}: {{ total('requests') }}</span><span>{{ t('serverless.errors') }}: {{ total('errors') }}</span></div></details>
      </template>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { getRegionalRouting, saveRegionalRouting, type RegionalConfig, type RegionalStatus } from '@/api/admin/serverless'
import { extractApiErrorMessage } from '@/utils/apiError'
import { serverlessMessages } from '@/components/admin/operations/serverlessMessages'
const { t } = useI18n({ useScope: 'local', messages: serverlessMessages })
const status = ref<RegionalStatus | null>(null), draft = ref<RegionalConfig | null>(null), busy = ref(false), error = ref(''), notice = ref('')
const runtimeFields = [{ key: 'secret_configured', label: 'serverless.secret' }, { key: 'redis_configured', label: 'serverless.redis' }, { key: 'private_network_configured', label: 'serverless.private' }] as const
let alive = true, sequence = 0
const nodeChoices = (selected: string[]) => [...new Set([...(draft.value?.pods.map(pod => pod.id.trim()).filter(Boolean) || []), ...selected])]
function toggleNode(region: RegionalConfig['regions'][number], id: string, checked: boolean) { region.pod_ids = checked ? [...new Set([...region.pod_ids, id])] : region.pod_ids.filter(value => value !== id) }
function removeNode(index: number) {
  if (!draft.value) return
  const id = draft.value.pods[index]?.id
  if (id && draft.value.regions.some(region => region.pod_ids.includes(id))) { error.value = t('serverless.nodeStillReferenced'); return }
  draft.value.pods.splice(index, 1)
}
const total = (suffix: string) => Object.entries(status.value?.stats || {}).reduce((sum, [key, value]) => sum + (key.endsWith('|' + suffix) ? Number(value) || 0 : 0), 0)
function assign(value: RegionalStatus) { status.value = value; draft.value = structuredClone(value.config) }
async function load() { if (busy.value) return; busy.value = true; const current = ++sequence; error.value = ''; try { const value = await getRegionalRouting(); if (alive && current === sequence) assign(value) } catch (e) { if (alive && current === sequence) error.value = extractApiErrorMessage(e, t('serverless.failed')) } finally { if (alive && current === sequence) busy.value = false } }
async function save() { if (busy.value || !draft.value) return; busy.value = true; error.value = notice.value = ''; try { const value = await saveRegionalRouting(draft.value); if (alive) { assign(value); notice.value = t('serverless.saved') } } catch (e) { if (alive) error.value = extractApiErrorMessage(e, t('serverless.failed')) } finally { if (alive) busy.value = false } }
onMounted(load)
onBeforeUnmount(() => { alive = false; sequence++ })
</script>
