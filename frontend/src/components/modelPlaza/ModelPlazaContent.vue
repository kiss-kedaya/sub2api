<template>
  <div class="model-plaza-content space-y-5">
    <!-- 页头(独立形态下展示标题;后台形态 AppHeader 已有页面标题) -->
    <div v-if="!embedded" class="plaza-heading">
      <h1>{{ t('modelPlaza.title') }}</h1>
      <p>{{ t('modelPlaza.description') }}</p>
    </div>

    <!-- 全局价格说明(管理员配置,Markdown) -->
    <div
      v-if="descriptionHtml"
      class="plaza-description px-1 py-3 text-sm"
      v-html="descriptionHtml"
    ></div>

    <!-- 未登录提示 -->
    <p
      v-if="!isAuthenticated"
      class="plaza-hint"
    >
      <Icon name="infoCircle" size="xs" class="h-3.5 w-3.5" />
      {{ t('modelPlaza.anonymousHint') }}
    </p>

    <!-- 加载/错误/空 -->
    <div v-if="loading" class="flex min-h-[240px] items-center justify-center">
      <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-600/25 border-t-primary-600 dark:border-primary-400/25 dark:border-t-primary-400"></div>
    </div>
    <div
      v-else-if="error"
      class="plaza-status plaza-status-error"
    >
      {{ t('modelPlaza.loadFailed') }}
    </div>
    <template v-else>
      <!-- 筛选区:平台 → 分组 → 倍率 -->
      <PlazaFilterBar
        :platforms="platforms"
        :groups="groupOptions"
        :rates="rates"
        :platform="selectedPlatform"
        :group-id="selectedGroupId"
        :rate="selectedRate"
        :search="searchQuery"
        @update:platform="selectedPlatform = $event"
        @update:group-id="selectedGroupId = $event"
        @update:rate="selectedRate = $event"
        @update:search="searchQuery = $event"
      />

      <!-- 分组分节的模型清单(默认按生效倍率升序) -->
      <div v-if="filteredGroups.length > 0" class="space-y-5">
        <PlazaGroupSection v-for="g in filteredGroups" :key="g.id" :group="g" />
      </div>
      <div
        v-else
        class="plaza-status"
      >
        {{ searchActive ? t('modelPlaza.noSearchResult') : t('modelPlaza.empty') }}
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import PlazaFilterBar from './PlazaFilterBar.vue'
import PlazaGroupSection from './PlazaGroupSection.vue'
import type { ModelPlazaGroup, ModelPlazaResponse } from '@/api/modelPlaza'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{
  response: ModelPlazaResponse | null
  loading: boolean
  error?: boolean
  /** 后台内嵌形态(AppLayout 内):隐藏页头。 */
  embedded?: boolean
}>()

const { t } = useI18n()
const authStore = useAuthStore()
const isAuthenticated = computed(() => authStore.isAuthenticated)

const selectedPlatform = ref<string>('all')
const selectedGroupId = ref<number | 'all'>('all')
const selectedRate = ref<number | 'all'>('all')
const searchQuery = ref('')

const searchActive = computed(() => searchQuery.value.trim() !== '')

const descriptionHtml = computed(() => {
  const md = props.response?.description?.trim()
  if (!md) return ''
  return DOMPurify.sanitize(marked.parse(md) as string)
})

/** 生效倍率 = 用户专属倍率 ?? 分组默认倍率。 */
function effectiveRate(g: ModelPlazaGroup): number {
  return g.user_rate_multiplier ?? g.rate_multiplier
}

const platforms = computed(() =>
  [...new Set((props.response?.groups ?? []).map((g) => g.platform).filter(Boolean))].sort()
)

const groupOptions = computed(() =>
  (props.response?.groups ?? []).map((g) => ({
    id: g.id,
    name: g.name,
    platform: g.platform,
    rate: effectiveRate(g)
  }))
)

/** 全量生效倍率;当前组合下不可用的项由 FilterBar 置灰而非隐藏。 */
const rates = computed(() =>
  [...new Set((props.response?.groups ?? []).map(effectiveRate))].sort((a, b) => a - b)
)

/** 数据刷新后选中的倍率可能不复存在,重置为全部。 */
watch(rates, (list) => {
  if (selectedRate.value !== 'all' && !list.includes(selectedRate.value)) {
    selectedRate.value = 'all'
  }
})

const filteredGroups = computed(() => {
  let groups = props.response?.groups ?? []
  if (selectedPlatform.value !== 'all') {
    groups = groups.filter((g) => g.platform === selectedPlatform.value)
  }
  if (selectedGroupId.value !== 'all') {
    groups = groups.filter((g) => g.id === selectedGroupId.value)
  }
  if (selectedRate.value !== 'all') {
    groups = groups.filter((g) => effectiveRate(g) === selectedRate.value)
  }
  // 模型名搜索:分组内只留命中的模型,整组无命中则隐藏该分组。
  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    groups = groups
      .map((g) => ({ ...g, models: g.models.filter((m) => m.name.toLowerCase().includes(q)) }))
      .filter((g) => g.models.length > 0)
  }
  // 专属倍率会改变生效值,不能只依赖后端按默认倍率的排序。
  return [...groups].sort(
    (a, b) => effectiveRate(a) - effectiveRate(b) || a.name.localeCompare(b.name)
  )
})
</script>

<style scoped>
.model-plaza-content {
  min-width: 0;
  color: var(--signal-text, #1c1c1e);
}

.plaza-heading h1 {
  font-size: 28px;
  line-height: 34px;
  font-weight: 600;
  letter-spacing: 0;
}

.plaza-heading p,
.plaza-hint {
  margin-top: 6px;
  color: var(--signal-muted, #63636c);
  font-size: 13px;
}

.plaza-hint {
  display: flex;
  align-items: center;
  gap: 6px;
}

.plaza-status {
  padding: 32px 20px;
  border: 1px dashed var(--signal-line, #dedee4);
  border-radius: 8px;
  color: var(--signal-muted, #63636c);
  text-align: center;
  font-size: 14px;
}

.plaza-status-error {
  border-style: solid;
  border-color: #eb4d3d55;
  background: #eb4d3d0f;
  color: #c4291c;
}

.plaza-description {
  border-bottom: 1px solid var(--signal-line, #dedee4);
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.plaza-description :deep(h1),
.plaza-description :deep(h2),
.plaza-description :deep(h3) {
  @apply mb-2 mt-3 font-semibold text-gray-900 first:mt-0 dark:text-white;
}

.plaza-description :deep(p) {
  @apply mb-2 text-gray-700 last:mb-0 dark:text-dark-200;
}

.plaza-description :deep(a) {
  @apply text-primary-600 underline underline-offset-4 hover:text-primary-700 dark:text-primary-300;
}

.plaza-description :deep(ul) {
  @apply mb-2 list-disc pl-5;
}

.plaza-description :deep(ol) {
  @apply mb-2 list-decimal pl-5;
}

.plaza-description :deep(li) {
  @apply mb-0.5 text-gray-700 dark:text-dark-200;
}

.plaza-description :deep(code) {
  @apply rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs dark:bg-dark-800;
}

.plaza-description :deep(blockquote) {
  @apply my-2 border-l-4 border-gray-300 pl-3 text-gray-600 dark:border-dark-600 dark:text-dark-300;
}
</style>
