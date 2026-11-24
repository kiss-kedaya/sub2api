<script setup lang="ts">
// 「页面已更新，必须刷新」：创建 Key 之类的敏感操作发现页面过期时弹出。
// 不能关：旧页面继续操作多半会失败，让用户先刷新比发一个注定失败的请求更省事。
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppUpdate } from '@/composables/useAppUpdate'

const { t } = useI18n()
const { blockingOpen, reloadNow } = useAppUpdate()
</script>

<template>
  <BaseDialog
    :show="blockingOpen"
    :title="t('appUpdate.dialogTitle')"
    :description="t('appUpdate.dialogDescription')"
    width="narrow"
    :z-index="70"
    :close-on-escape="false"
    :close-on-click-outside="false"
    :show-close-button="false"
  >
    <p class="text-sm text-muted" data-testid="app-update-dialog-hint">
      {{ t('appUpdate.dialogHint') }}
    </p>
    <template #footer>
      <div class="flex justify-end">
        <UButton
          type="button"
          size="lg"
          icon="i-lucide-refresh-cw"
          :label="t('appUpdate.dialogAction')"
          data-testid="app-update-dialog-refresh"
          @click="reloadNow"
        />
      </div>
    </template>
  </BaseDialog>
</template>
