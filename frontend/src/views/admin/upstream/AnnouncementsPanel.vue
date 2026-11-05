<template>
  <div class="flex flex-col gap-4">
    <PageToolbar>
      <template #left>
        <USelect v-model="channelId" :items="channelItems" class="min-w-48" :aria-label="text('上游', 'Upstream')" />
      </template>
      <template #right>
        <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="text('刷新', 'Refresh')" :loading="loading" @click="load(1)" />
        <UButton icon="i-lucide-cloud-download" :loading="syncing" :disabled="!channelId" :label="text('立即同步', 'Sync now')" @click="sync" />
      </template>
    </PageToolbar>

    <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
    <UAlert v-if="notice" color="success" variant="subtle" icon="i-lucide-check-circle-2" :title="notice" />

    <p class="text-xs text-muted">
      {{ text('同步只读取上游公告列表，新增的公告会通过统一的 Bark / 邮件通知中心推送一次；首次同步只建立基线，不会把历史公告全推一遍。', 'Sync only reads the upstream announcement list. New announcements are pushed once through the shared Bark / email hub; the first sync seeds a baseline without replaying history.') }}
    </p>

    <div class="space-y-2">
      <article v-for="item in rows" :key="item.id" class="card p-4">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0">
            <p class="font-medium text-highlighted">{{ item.title || text('（无标题）', '(no title)') }}</p>
            <p class="mt-1 text-xs text-muted">
              {{ item.channel_name || `#${item.channel_id}` }}
              <span v-if="item.published_at"> · {{ text('发布', 'published') }} {{ formatTime(item.published_at) }}</span>
              <span> · {{ text('首次看到', 'first seen') }} {{ formatTime(item.first_seen_at) }}</span>
            </p>
          </div>
          <div class="flex items-center gap-1">
            <UBadge v-if="item.notify_mode" size="sm" variant="subtle" color="neutral">{{ item.notify_mode }}</UBadge>
            <UBadge :color="item.pushed_at ? 'success' : 'neutral'" size="sm" variant="subtle">
              {{ item.pushed_at ? text('已推送', 'pushed') : text('未推送', 'pending') }}
            </UBadge>
          </div>
        </div>
        <p class="mt-2 whitespace-pre-line text-sm" :class="expanded.has(item.id) ? '' : 'line-clamp-3'">{{ item.content }}</p>
        <UButton
          v-if="item.content && item.content.length > 160"
          size="xs"
          color="neutral"
          variant="link"
          class="mt-1 px-0"
          :label="expanded.has(item.id) ? text('收起', 'Show less') : text('展开全文', 'Show more')"
          @click="toggle(item.id)"
        />
      </article>
      <p v-if="!rows.length && !loading" class="card p-8 text-center text-sm text-muted">
        {{ text('还没有同步过公告。选一个上游，点「立即同步」。', 'No announcements synced yet. Pick an upstream and hit "Sync now".') }}
      </p>
    </div>

    <div class="flex flex-wrap items-center justify-between gap-3 text-sm text-muted">
      <span>{{ text(`${total} 条公告`, `${total} announcements`) }}</span>
      <div class="flex items-center gap-2">
        <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" :disabled="loading || page <= 1" :aria-label="text('上一页', 'Previous page')" @click="load(page - 1)" />
        <span class="tabular-nums">{{ page }} / {{ pages || 1 }}</span>
        <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" :disabled="loading || page >= pages" :aria-label="text('下一页', 'Next page')" @click="load(page + 1)" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import {
  listChannelAnnouncements,
  listChannels,
  syncChannelAnnouncements,
  type UpstreamAnnouncement,
  type UpstreamChannel,
} from '@/api/admin/upstreamChannel'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const channels = ref<UpstreamChannel[]>([])
const channelId = ref<number | null>(null)
const rows = ref<UpstreamAnnouncement[]>([])
const total = ref(0)
const page = ref(1)
const pages = ref(1)
const pageSize = 20
const loading = ref(false)
const syncing = ref(false)
const error = ref('')
const notice = ref('')
const expanded = ref(new Set<number>())

const channelItems = ref<{ label: string; value: number | null }[]>([{ label: text('全部上游', 'All upstreams'), value: null }])

function formatTime(value?: string | null): string {
  return value ? new Date(value).toLocaleString() : '—'
}
function reason(err: unknown, fallback: string): string {
  const candidate = err as { message?: string }
  return candidate?.message || fallback
}
function toggle(id: number) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

async function load(pageValue = page.value) {
  loading.value = true
  error.value = ''
  try {
    const result = await listChannelAnnouncements(channelId.value ?? 0, { page: pageValue, pageSize })
    rows.value = result.items ?? []
    total.value = result.total ?? 0
    pages.value = result.pages ?? 1
    page.value = result.page ?? pageValue
  } catch (err) {
    error.value = reason(err, text('读取公告失败', 'Failed to load announcements'))
  } finally {
    loading.value = false
  }
}

async function loadChannels() {
  try {
    const result = await listChannels({ page: 1, pageSize: 100 })
    channels.value = result.items ?? []
    channelItems.value = [
      { label: text('全部上游', 'All upstreams'), value: null },
      ...channels.value.map(channel => ({ label: channel.name, value: channel.id })),
    ]
  } catch (err) {
    error.value = reason(err, text('读取上游列表失败', 'Failed to load upstreams'))
  }
}

async function sync() {
  if (!channelId.value) return
  syncing.value = true
  error.value = ''
  try {
    const result = await syncChannelAnnouncements(channelId.value)
    notice.value = text(
      `同步完成：共 ${result.fetched} 条，新增 ${result.added}，更新 ${result.updated}，首次基线与基线外 ${result.seeded}，推送 ${result.pushed}`,
      `Synced ${result.fetched}: ${result.added} new, ${result.updated} updated, ${result.seeded} seeded, ${result.pushed} pushed`
    )
    await load(1)
  } catch (err) {
    error.value = reason(err, text('同步失败，先确认该上游已登录', 'Sync failed; make sure the upstream is signed in'))
  } finally {
    syncing.value = false
  }
}

watch(channelId, () => {
  page.value = 1
  void load(1)
})

onMounted(() => {
  void loadChannels()
  void load(1)
})
</script>
