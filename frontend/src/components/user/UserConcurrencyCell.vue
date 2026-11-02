<template>
  <div class="flex min-w-0 items-center">
    <UPopover
      v-model:open="open"
      mode="hover"
      :content="{ side: 'bottom', align: 'start' }"
    >
      <span tabindex="0" class="inline-flex cursor-help" :aria-label="t('admin.users.concurrencyHint')">
        <CapacityMeterBadge
          :label="t('admin.users.concurrencyLabel')"
          :hint="t('admin.users.concurrencyHint')"
          :used="current"
          :max="max"
          icon="i-lucide-layout-grid"
        />
      </span>
      <template #content>
        <div class="w-72 space-y-2 px-3 py-3 text-xs" data-test="user-live-load">
          <div class="flex items-center justify-between gap-3">
            <span class="font-medium text-highlighted">{{ t('admin.users.concurrencyLabel') }}</span>
            <span class="font-mono tabular-nums text-default">{{ current }} / {{ max || '∞' }}</span>
          </div>
          <div v-if="load?.waiting_count" class="text-warning">
            {{ t('admin.users.liveLoad.waiting', { count: load.waiting_count }) }}
          </div>

          <div v-if="loading" class="py-1 text-muted">
            {{ t('admin.users.liveLoad.loading') }}
          </div>
          <div v-else-if="error" class="py-1 text-error">
            {{ t('admin.users.liveLoad.failed') }}
          </div>
          <div v-else-if="groups.length > 0" class="space-y-1.5">
            <div
              v-for="group in groups"
              :key="group.group_id"
              class="rounded-md border border-default/60 px-2 py-1.5"
              :data-test="`user-live-load-group-${group.group_id}`"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="truncate font-medium text-default">
                  {{ group.group_name || (group.group_id ? `#${group.group_id}` : t('admin.users.liveLoad.unknownGroup')) }}
                </span>
                <span class="shrink-0 font-mono tabular-nums text-highlighted">{{ group.inflight }}</span>
              </div>
              <div v-if="group.models?.length" class="mt-0.5 truncate text-[10px] text-dimmed">
                {{ group.models.join(' · ') }}
              </div>
            </div>
          </div>
          <div v-else class="py-1 text-muted">
            {{ t('admin.users.liveLoad.empty') }}
          </div>

          <p class="border-t border-default/60 pt-2 text-[10px] leading-4 text-dimmed">
            {{ t('admin.users.liveLoad.hint') }}
          </p>
        </div>
      </template>
    </UPopover>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import CapacityMeterBadge from '@/components/common/CapacityMeterBadge.vue'
import { usersAPI } from '@/api/admin'
import type { UserGroupLoad, UserLoadResponse } from '@/api/admin/users'

const { t } = useI18n()

const props = defineProps<{
  userId: number
  current: number
  max: number
}>()

const open = ref(false)
const loading = ref(false)
const error = ref(false)
const load = ref<UserLoadResponse | null>(null)

const groups = computed<UserGroupLoad[]>(() => load.value?.groups ?? [])

// Hover is a read-only side channel. Cache for 15s so moving the pointer across
// neighbouring rows does not produce a burst of identical requests, and drop
// the result when the row points at another user.
const CACHE_TTL_MS = 15_000
const cache = new Map<number, { at: number; value: UserLoadResponse }>()
let requestSeq = 0

async function ensureLoaded(force = false) {
  if (!open.value || props.userId <= 0) return
  const cached = cache.get(props.userId)
  if (!force && cached && Date.now() - cached.at < CACHE_TTL_MS) {
    load.value = cached.value
    error.value = false
    return
  }
  const seq = ++requestSeq
  loading.value = true
  error.value = false
  try {
    const value = await usersAPI.getLoad(props.userId)
    if (seq !== requestSeq) return
    cache.set(props.userId, { at: Date.now(), value })
    load.value = value
  } catch {
    if (seq !== requestSeq) return
    error.value = true
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

watch(open, (value) => {
  if (value) {
    void ensureLoaded()
  }
})

watch(() => props.userId, () => {
  load.value = null
  error.value = false
})

// Exposed for the lazy-load regression test; not part of the public UI API.
defineExpose({ open, load, ensureLoaded })
</script>
