<template>
  <span v-if="!owner" class="text-muted" data-testid="crypto-owner-unknown">—</span>
  <div v-else class="flex max-w-40 min-w-0 flex-col leading-tight" data-testid="crypto-owner">
    <span class="flex min-w-0 items-center gap-1.5">
      <span class="truncate" :title="primary" data-testid="crypto-owner-primary">{{ primary }}</span>
      <UBadge v-if="owner.deleted" color="neutral" variant="subtle" size="sm" class="shrink-0" data-testid="crypto-owner-deleted">{{ t('admin.crypto.sweepView.ownerDeleted') }}</UBadge>
    </span>
    <span v-if="secondaryEmail || showId" class="flex min-w-0 items-center gap-1 text-xs text-muted" data-testid="crypto-owner-secondary">
      <span v-if="secondaryEmail" class="truncate" :title="secondaryEmail" data-testid="crypto-owner-email">{{ secondaryEmail }}</span>
      <span v-if="secondaryEmail && showId" aria-hidden="true">·</span>
      <span v-if="showId" class="shrink-0" data-testid="crypto-owner-id">#{{ owner.user_id }}</span>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AddressOwner } from '@/api/admin/crypto'

// Display only. `owner` is undefined/null while unknown (not looked up, failed,
// or genuinely unowned), which all read the same to the operator: "—".
const props = defineProps<{ owner?: AddressOwner | null }>()
const { t } = useI18n()

const username = computed(() => props.owner?.username?.trim() ?? '')
const email = computed(() => props.owner?.email?.trim() ?? '')
// username, then email, then #id.
const primary = computed(() => username.value || email.value || `#${props.owner?.user_id ?? ''}`)
// The email moves to the small line only when the username took the main one.
const secondaryEmail = computed(() => (username.value ? email.value : ''))
// #id is always visible; when it already is the main text there is no second copy.
const showId = computed(() => Boolean(username.value || email.value))
</script>
