<template>
  <section class="flex flex-col gap-4" aria-labelledby="country-filter-title">
    <UPageCard variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }" :description="text('机场节点按最近的出口 IP 检测结果筛选。动态代理每次连接可能更换 IP，历史检测结果不用于准入。', 'Subscription nodes use the last exit-IP country check. Dynamic proxies may change IP on every connection; previous checks do not determine eligibility.')">
      <template #title>
        <span class="flex flex-wrap items-center gap-2">
          <span id="country-filter-title">{{ text('国家／地区过滤', 'Country / region filter') }}</span>
          <UBadge color="neutral" variant="subtle">{{ text('预计可用', 'Eligible') }} {{ eligible }} / {{ nodes.length }}</UBadge>
        </span>
      </template>
      <UButton class="w-fit lg:ms-auto" :disabled="busy || (draft.mode !== 'off' && !draft.codes.length)" :label="text('保存地区规则', 'Save region rules')" @click="save" />
    </UPageCard>
    <UAlert v-if="dynamicBlocked" role="status" color="warning" variant="subtle" :description="text('动态代理未参与轮换：请启用供应商控制地区，或明确允许未知地区。重复检测不能保证下次连接的地区。', 'Dynamic proxies are excluded. Enable provider-managed regions or allow unknown regions; repeated checks cannot guarantee the next connection’s country.')" />
    <UAlert v-if="eligible === 0 && nodes.length" role="status" color="warning" variant="subtle" :description="text('应用后没有可参与打票的节点。可先检测地区或调整筛选；不会自动回退到被排除地区。', 'No eligible harvest nodes after applying. Check regions or adjust the filter; excluded regions are never a fallback.')" />
    <UPageCard variant="subtle">
      <UFormField :label="text('过滤模式', 'Filter mode')" class="flex max-sm:flex-col justify-between items-start gap-4">
        <div class="flex flex-wrap items-center justify-end gap-2">
          <USelect :model-value="draft.mode" :items="modeItems" class="min-w-48" :disabled="busy" @update:model-value="setMode" />
          <UButton color="neutral" variant="outline" :disabled="busy" :label="text('快捷：排除香港', 'Exclude Hong Kong')" @click="excludeHongKong" />
        </div>
      </UFormField>
      <template v-if="draft.mode !== 'off'">
        <USeparator />
        <UFormField :label="text('地区', 'Regions')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'sm:max-w-sm w-full' }">
          <div class="flex flex-col items-stretch gap-2">
            <USelectMenu
              :model-value="draft.codes"
              :items="options"
              value-key="value"
              multiple
              :filter-fields="['label', 'search']"
              :search-input="searchInput"
              :aria-label="text('地区选项', 'Region options')"
              icon="i-lucide-plus"
              :disabled="busy"
              class="w-full"
              @update:model-value="setCodes"
            >
              <span class="truncate text-dimmed">{{ text('添加或移除地区', 'Add or remove regions') }}</span>
            </USelectMenu>
            <div class="flex flex-wrap gap-1.5" aria-live="polite">
              <UButton v-for="code in draft.codes" :key="code" size="xs" color="primary" variant="soft" trailing-icon="i-lucide-x" :disabled="busy" :aria-label="text('移除 ', 'Remove ') + label(code)" :label="label(code)" @click="toggle(code)" />
              <span v-if="!draft.codes.length" class="text-xs text-warning">{{ text('请至少选择一个国家或地区', 'Select at least one country or region') }}</span>
            </div>
          </div>
        </UFormField>
        <USeparator />
        <UFormField :label="text('允许未知地区节点（尚未检测或检测失败）', 'Allow unknown regions (unchecked or failed checks)')" class="flex items-center justify-between gap-2">
          <USwitch v-model="draft.allow_unknown" :disabled="busy" @update:model-value="dirty = true" />
        </UFormField>
        <USeparator />
        <UFormField :label="text('动态代理地区由供应商控制（允许动态节点，不应用上方地区筛选）', 'Provider controls dynamic proxy regions (allow dynamic nodes without the region filter above)')" :description="text('开启后请在供应商侧限定地区；Random 不保证排除 CN/HK。关闭时动态节点按未知地区处理。', 'When enabled, restrict regions at your provider; Random does not guarantee exclusion of CN/HK. Otherwise dynamic nodes are treated as unknown.')" class="flex items-center justify-between gap-2">
          <USwitch v-model="draft.dynamic_provider_managed" data-testid="dynamic-provider-managed" :disabled="busy" @update:model-value="dirty = true" />
        </UFormField>
      </template>
      <USeparator />
      <UFormField :label="text('出口地区检测', 'Exit region check')" :description="text('检测通过节点访问 country.is 查询出口地区，每次至多 20 个最久未检测的节点，不调用模型。地区规则与手动停用、用后移出同时生效。', 'Checks exit regions via country.is through up to 20 least-recently checked nodes, without model calls. Region rules also apply to disabled and retired nodes.')" class="flex max-sm:flex-col justify-between items-start gap-4">
        <UButton color="neutral" variant="outline" icon="i-lucide-map-pin" :disabled="busy || !nodes.length" :label="text('检测出口地区', 'Check exit regions')" @click="$emit('scan')" />
      </UFormField>
    </UPageCard>
  </section>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CountryFilter, CountryNode } from './mihomoCountry'
const props = defineProps<{ filter?: CountryFilter; codes: string[]; nodes: CountryNode[]; busy: boolean }>()
const emit = defineEmits<{ save: [filter: CountryFilter]; scan: [] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const draft = reactive<CountryFilter>({ mode: 'off', codes: [], allow_unknown: false })
const dirty = ref(false)
watch(() => props.filter, value => {
  const next = value || { mode: 'off' as const, codes: [], allow_unknown: false }
  const equal = draft.mode === next.mode && draft.allow_unknown === next.allow_unknown && !!draft.dynamic_provider_managed === !!next.dynamic_provider_managed && [...draft.codes].sort().join(',') === [...(next.codes || [])].sort().join(',')
  if (!dirty.value || equal) { Object.assign(draft, { dynamic_provider_managed: false }, next, { codes: [...(next.codes || [])] }); dirty.value = false }
}, { immediate: true, deep: true })
const names = computed(() => new Intl.DisplayNames([locale.value.startsWith('zh') ? 'zh' : 'en'], { type: 'region' }))
const englishNames = new Intl.DisplayNames(['en'], { type: 'region' })
const label = (code: string) => `${code === 'HK' ? text('香港', 'Hong Kong') : names.value.of(code) || code} ${code}`
const common = ['HK', 'CN', 'MO', 'US', 'JP', 'SG', 'TW', 'KR', 'GB', 'DE', 'CA', 'AU']
const modeItems = computed(() => [
  { label: text('不限制地区', 'No region filter'), value: 'off' as const },
  { label: text('排除所选地区', 'Exclude selected regions'), value: 'exclude' as const },
  { label: text('仅允许所选地区', 'Allow selected regions only'), value: 'include' as const },
])
// Common regions first; the menu filters on the localized label and the English name (search field).
const options = computed(() => [...new Set([...common.filter(code => props.codes.includes(code)), ...props.codes])]
  .map(code => ({ value: code, label: label(code), search: `${label(code)} ${englishNames.of(code) || ''}` })))
// The search field's attrs reach its native input element (InputProps omits aria-* from its type, so build it here).
const searchInput = computed(() => ({ placeholder: text('搜索地区名称或代码，例如 香港、Hong Kong、HK', 'Search a region or code, e.g. Hong Kong, HK'), 'aria-label': text('搜索国家地区', 'Search countries and regions') } as Record<string, string>))
function setMode(mode: CountryFilter['mode']) { dirty.value = true; draft.mode = mode }
function setCodes(codes: string[]) { dirty.value = true; draft.codes = [...codes] }
function toggle(code: string) { dirty.value = true; draft.codes = draft.codes.includes(code) ? draft.codes.filter(c => c !== code) : [...draft.codes, code] }
function excludeHongKong() { dirty.value = true; draft.codes = draft.mode === 'exclude' ? [...new Set([...draft.codes, 'HK'])] : ['HK']; draft.mode = 'exclude' }
function save() { emit('save', { ...draft, codes: [...draft.codes] }) }
const eligible = computed(() => props.nodes.filter(node => {
  if (node.state !== 'enabled' && node.state !== 'country_excluded') return false
  if (draft.mode === 'off') return true
  if (node.dynamic) return draft.dynamic_provider_managed || draft.allow_unknown
  if (!node.country_code) return draft.allow_unknown
  const selected = draft.codes.includes(node.country_code)
  return draft.mode === 'include' ? selected : !selected
}).length)
const dynamicBlocked = computed(() => props.nodes.some(n => n.dynamic) && draft.mode !== 'off' && !draft.dynamic_provider_managed && !draft.allow_unknown)
</script>
