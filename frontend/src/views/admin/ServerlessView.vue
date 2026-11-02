<template>
  <AppLayout>
    <div class="mx-auto flex w-full flex-col gap-4 sm:gap-6 lg:max-w-2xl">
      <AdminPageHeader :title="t('serverless.title')" :description="t('serverless.description')"><template #actions><UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="t('serverless.refresh')" :disabled="busy" @click="load" /></template></AdminPageHeader>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      <UAlert v-if="notice" role="status" color="success" variant="subtle" icon="i-lucide-circle-check" :title="notice" />
      <template v-if="status && draft">
        <form id="serverless-routing" class="flex flex-col gap-4 sm:gap-6" @submit.prevent="save">
          <div>
            <UPageCard :title="t('serverless.assignment')" :description="t('serverless.continuity')" variant="naked" orientation="horizontal" class="mb-4" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
              <UButton type="submit" form="serverless-routing" :label="t('serverless.save')" :disabled="busy" :loading="busy" class="w-fit lg:ms-auto" />
            </UPageCard>
            <UPageCard variant="subtle">
              <UFormField :label="t('serverless.enabled')" :description="t('serverless.setup')" class="flex items-center justify-between gap-4">
                <USwitch v-model="draft.enabled" data-testid="serverless-enabled" :disabled="!status.runtime.secret_configured || !status.runtime.redis_configured" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('serverless.runtime')" class="flex max-sm:flex-col justify-between items-start gap-4">
                <div class="flex flex-wrap gap-1.5 sm:justify-end">
                  <UBadge v-for="field in runtimeFields" :key="field.key" :color="status.runtime[field.key] ? 'success' : 'neutral'" variant="subtle">{{ t(field.label) }}: {{ status.runtime[field.key] ? t('serverless.yes') : t('serverless.no') }}</UBadge>
                </div>
              </UFormField>
            </UPageCard>
          </div>
          <div>
            <UPageCard :title="t('serverless.nodes')" variant="naked" orientation="horizontal" class="mb-4" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
              <UButton type="button" icon="i-lucide-plus" color="neutral" variant="outline" :label="t('serverless.add')" class="w-fit lg:ms-auto" @click="draft.pods.push({ id: '', endpoint: '', enabled: false })" />
            </UPageCard>
            <UPageCard variant="subtle">
              <p v-if="!draft.pods.length" class="text-sm text-muted">{{ t('serverless.addNodesFirst') }}</p>
              <template v-for="(pod, index) in draft.pods" :key="index">
                <USeparator v-if="index" />
                <div class="grid items-end gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto]">
                  <UFormField :label="t('serverless.nodeId')"><UInput v-model.trim="pod.id" maxlength="63" required class="w-full" /></UFormField>
                  <UFormField :label="t('serverless.endpoint')"><UInput v-model.trim="pod.endpoint" type="url" maxlength="2048" required class="w-full" /></UFormField>
                  <UButton type="button" icon="i-lucide-trash-2" color="error" variant="ghost" :aria-label="t('serverless.remove')" :title="t('serverless.remove')" @click="removeNode(index)" />
                </div>
                <USwitch v-model="pod.enabled" size="sm" :label="t('serverless.available')" />
              </template>
            </UPageCard>
          </div>
          <div>
            <UPageCard :title="t('serverless.regions')" :description="t('serverless.countriesHint')" variant="naked" orientation="horizontal" class="mb-4" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
              <UButton type="button" icon="i-lucide-plus" color="neutral" variant="outline" :label="t('serverless.add')" class="w-fit lg:ms-auto" @click="draft.regions.push({ country: '', pod_ids: [] })" />
            </UPageCard>
            <UPageCard v-if="draft.regions.length" variant="subtle">
              <template v-for="(region, index) in draft.regions" :key="index">
                <USeparator v-if="index" />
                <div class="flex flex-wrap items-start gap-4">
                  <UFormField :label="t('serverless.country')" class="w-24"><UInput :model-value="region.country" class="w-full" :ui="{ base: 'uppercase' }" maxlength="2" pattern="[A-Z]{2}" required @update:model-value="region.country = String($event ?? '').toUpperCase().trim()" /></UFormField>
                  <fieldset class="min-w-48 flex-1 text-sm">
                    <legend class="mb-2 font-medium text-default">{{ t('serverless.ids') }}</legend>
                    <div class="flex flex-wrap gap-x-4 gap-y-2">
                      <UCheckbox v-for="id in nodeChoices(region.pod_ids)" :key="id" :label="id" :model-value="region.pod_ids.includes(id)" @update:model-value="toggleNode(region, id, $event === true)" />
                      <span v-if="!nodeChoices(region.pod_ids).length" class="text-muted">{{ t('serverless.addNodesFirst') }}</span>
                    </div>
                  </fieldset>
                  <UButton type="button" icon="i-lucide-trash-2" color="error" variant="ghost" class="mt-6" :aria-label="t('serverless.remove')" :title="t('serverless.remove')" @click="draft.regions.splice(index, 1)" />
                </div>
              </template>
            </UPageCard>
          </div>
        </form>
        <div>
          <UPageCard :title="t('serverless.heartbeat')" variant="naked" class="mb-4" />
          <UPageCard variant="subtle">
            <p v-if="!status.pods.length" class="text-sm text-muted">{{ t('serverless.empty') }}</p>
            <template v-for="(pod, index) in status.pods" :key="pod.id">
              <USeparator v-if="index" />
              <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
                <span class="font-medium text-highlighted">{{ pod.id }} <span class="font-normal text-muted">· {{ pod.region || '—' }}</span></span>
                <span class="flex items-center gap-2">
                  <UBadge :color="pod.ready ? 'success' : 'neutral'" variant="subtle">{{ t('serverless.ready') }}: {{ pod.ready ? t('serverless.yes') : t('serverless.no') }}</UBadge>
                  <span class="text-muted">{{ t('serverless.version') }}: {{ pod.version }}</span>
                </span>
              </div>
            </template>
          </UPageCard>
        </div>
        <UCollapsible v-model:open="statsOpen" class="flex flex-col gap-3">
          <UButton color="neutral" variant="ghost" class="-mx-2.5 w-fit" :label="t('serverless.advanced')" :trailing-icon="statsOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" />
          <template #content>
            <UPageGrid class="gap-4 sm:grid-cols-2 lg:grid-cols-2">
              <UPageCard v-for="key in (['requests', 'errors'] as const)" :key="key" variant="subtle" :title="t('serverless.' + key)" :ui="{ container: 'gap-y-1.5', title: 'font-normal text-muted text-xs uppercase' }">
                <span class="text-2xl font-semibold text-highlighted">{{ total(key) }}</span>
              </UPageCard>
            </UPageGrid>
          </template>
        </UCollapsible>
      </template>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { getRegionalRouting, saveRegionalRouting, type RegionalConfig, type RegionalStatus } from '@/api/admin/serverless'
import { extractApiErrorMessage } from '@/utils/apiError'
import { serverlessMessages } from '@/components/admin/operations/serverlessMessages'
const { t } = useI18n({ useScope: 'local', messages: serverlessMessages })
const status = ref<RegionalStatus | null>(null), draft = ref<RegionalConfig | null>(null), busy = ref(false), error = ref(''), notice = ref(''), statsOpen = ref(false)
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
