<template>
  <section
    class="rounded-xl border border-amber-300 bg-amber-50 p-5 text-amber-950 shadow-sm sm:p-6 dark:border-amber-400/40 dark:bg-amber-400/10 dark:text-amber-50"
    role="alert"
    data-testid="key-rotation-gate-notice"
  >
    <div class="flex items-start gap-4">
      <div class="flex size-11 shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-400/20">
        <UIcon name="i-lucide-shield-alert" class="size-6 text-amber-600 dark:text-amber-300" aria-hidden="true" />
      </div>
      <div class="min-w-0 space-y-3">
        <div class="space-y-1">
          <h2 class="text-base font-semibold">{{ t('keyRotationGate.title') }}</h2>
          <p class="text-sm leading-6">{{ t('keyRotationGate.description') }}</p>
        </div>

        <dl class="flex flex-wrap gap-x-8 gap-y-2 text-xs">
          <div v-if="activeCount >= 0">
            <dt class="font-medium text-amber-700 dark:text-amber-200">{{ t('keyRotationGate.activeKeysLabel') }}</dt>
            <dd class="mt-0.5 tabular-nums">{{ activeCount }}</dd>
          </div>
          <div v-if="pendingCount > 0">
            <dt class="font-medium text-amber-700 dark:text-amber-200">{{ t('keyRotationGate.pendingKeysLabel') }}</dt>
            <dd class="mt-0.5 tabular-nums">{{ pendingCount }}</dd>
          </div>
        </dl>

        <p class="text-xs leading-5 text-amber-800 dark:text-amber-100/80">
          {{ t('keyRotationGate.hint') }}
        </p>

        <UButton
          :to="remediationPath"
          size="lg"
          color="warning"
          icon="i-lucide-key-round"
          data-testid="key-rotation-gate-action"
        >
          {{ t('keyRotationGate.action') }}
        </UButton>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { KeyRotationGateStatus } from '@/api/keyRotationGate'
import { KEY_ROTATION_REMEDIATION_PATH } from '@/api/keyRotationGateCodes'

const props = defineProps<{
  status?: KeyRotationGateStatus | null
}>()

const { t } = useI18n()

const remediationPath = computed(() => props.status?.remediation_path || KEY_ROTATION_REMEDIATION_PATH)

const activeCount = computed(() => props.status?.active_key_count ?? -1)
const pendingCount = computed(() => props.status?.pending_key_count ?? 0)
</script>
