<template>
  <USelectMenu
    v-model:open="isOpen"
    v-model:search-term="searchQuery"
    :model-value="modelValue === null ? NO_PROXY : modelValue"
    :items="items"
    value-key="value"
    ignore-filter
    :disabled="disabled"
    :search-input="{ placeholder: t('admin.proxies.searchProxies'), icon: 'i-lucide-search' }"
    class="w-full"
    :ui="{ content: 'min-w-80' }"
    data-test="proxy-selector"
    @update:model-value="selectOption"
  >
    <template #default>
      <span class="truncate">{{ selectedLabel }}</span>
    </template>

    <template #content-top>
      <div v-if="proxies.length > 0" class="flex items-center justify-between gap-2 px-2.5 pt-1.5">
        <span class="text-xs text-muted">{{ t('admin.proxies.batchTest') }}</span>
        <UButton
          color="neutral"
          variant="ghost"
          size="xs"
          icon="i-lucide-play"
          :loading="batchTesting"
          :title="t('admin.proxies.batchTest')"
          :aria-label="t('admin.proxies.batchTest')"
          data-test="proxy-batch-test"
          @click.stop="handleBatchTest"
        />
      </div>
    </template>

    <template #item-label="{ item }">
      <span v-if="(item as ProxyItem).proxy === null" class="truncate">{{ t('admin.accounts.noProxy') }}</span>
      <span v-else class="block min-w-0">
        <span class="flex min-w-0 items-center gap-1.5">
          <span class="truncate font-medium">{{ (item as ProxyItem).proxy!.name }}</span>
          <!-- Account count badge -->
          <UBadge
            v-if="(item as ProxyItem).proxy!.account_count !== undefined"
            color="neutral"
            variant="subtle"
            size="sm"
            :label="String((item as ProxyItem).proxy!.account_count)"
          />
          <!-- Test result badges -->
          <template v-if="testResults[(item as ProxyItem).proxy!.id]">
            <UBadge
              v-if="testResults[(item as ProxyItem).proxy!.id].success"
              color="success"
              variant="subtle"
              size="sm"
              class="gap-1 tabular-nums"
            >
              <span v-if="testResults[(item as ProxyItem).proxy!.id].country">{{ testResults[(item as ProxyItem).proxy!.id].country }}</span>
              <span v-if="testResults[(item as ProxyItem).proxy!.id].latency_ms">{{ testResults[(item as ProxyItem).proxy!.id].latency_ms }}ms</span>
            </UBadge>
            <UBadge v-else color="error" variant="subtle" size="sm" :label="t('admin.proxies.testFailed')" />
          </template>
        </span>
        <span class="block truncate text-xs text-muted">
          {{ (item as ProxyItem).proxy!.protocol }}://{{ (item as ProxyItem).proxy!.host }}:{{ (item as ProxyItem).proxy!.port }}
        </span>
      </span>
    </template>

    <template #item-trailing="{ item }">
      <!-- Individual test button; does not select the row -->
      <UButton
        v-if="(item as ProxyItem).proxy"
        color="neutral"
        variant="ghost"
        size="xs"
        icon="i-lucide-play"
        :loading="testingProxyIds.has((item as ProxyItem).proxy!.id)"
        :title="t('admin.proxies.testConnection')"
        :aria-label="t('admin.proxies.testConnection')"
        data-test="proxy-test"
        @click.stop="handleTestProxy((item as ProxyItem).proxy!)"
        @keydown.enter.stop
        @keydown.space.stop
      />
    </template>

    <template #empty>
      <span class="text-sm text-muted">{{ t('common.noOptionsFound') }}</span>
    </template>
  </USelectMenu>
</template>
<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Proxy } from '@/types'

const { t } = useI18n()

interface ProxyTestResult {
  success: boolean
  message: string
  latency_ms?: number
  ip_address?: string
  city?: string
  region?: string
  country?: string
}

interface Props {
  modelValue: number | null
  proxies: Proxy[]
  disabled?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false
})

const emit = defineEmits<{
  'update:modelValue': [value: number | null]
}>()

/** The menu needs a non-null value for "no proxy"; it maps back to null. */
const NO_PROXY = 'none'
type ProxyItem = { value: number | typeof NO_PROXY; label: string; proxy: Proxy | null }

const isOpen = ref(false)
const searchQuery = ref('')

// Test state
const testResults = reactive<Record<number, ProxyTestResult>>({})
const testingProxyIds = reactive(new Set<number>())
const batchTesting = ref(false)

const selectedProxy = computed(() => {
  if (props.modelValue === null) return null
  return props.proxies.find((p) => p.id === props.modelValue) || null
})

const selectedLabel = computed(() => {
  if (!selectedProxy.value) {
    return t('admin.accounts.noProxy')
  }
  const proxy = selectedProxy.value
  return `${proxy.name} (${proxy.protocol}://${proxy.host}:${proxy.port})`
})

const filteredProxies = computed(() => {
  if (!searchQuery.value) {
    return props.proxies
  }
  const query = searchQuery.value.toLowerCase()
  return props.proxies.filter((proxy) => {
    const name = proxy.name.toLowerCase()
    const host = proxy.host.toLowerCase()
    return name.includes(query) || host.includes(query)
  })
})

// "No proxy" stays on top while searching; an empty search result shows the empty state.
const items = computed<ProxyItem[]>(() => {
  const proxyItems = filteredProxies.value.map((proxy) => ({ value: proxy.id, label: proxy.name, proxy }))
  if (searchQuery.value && proxyItems.length === 0) return []
  return [{ value: NO_PROXY, label: t('admin.accounts.noProxy'), proxy: null }, ...proxyItems]
})

watch(isOpen, (open) => {
  if (!open) searchQuery.value = ''
})

const selectOption = (value: unknown) => {
  emit('update:modelValue', value === NO_PROXY || value == null ? null : Number(value))
  isOpen.value = false
  searchQuery.value = ''
}

const handleTestProxy = async (proxy: Proxy) => {
  if (testingProxyIds.has(proxy.id)) return

  testingProxyIds.add(proxy.id)
  try {
    const result = await adminAPI.proxies.testProxy(proxy.id)
    testResults[proxy.id] = result
  } catch (error: any) {
    testResults[proxy.id] = {
      success: false,
      message: error.response?.data?.detail || 'Test failed'
    }
  } finally {
    testingProxyIds.delete(proxy.id)
  }
}

const handleBatchTest = async () => {
  if (batchTesting.value || props.proxies.length === 0) return

  batchTesting.value = true

  // Test all proxies in parallel
  const testPromises = props.proxies.map(handleTestProxy)

  await Promise.all(testPromises)
  batchTesting.value = false
}
</script>
