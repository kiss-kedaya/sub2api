import { computed, ref } from 'vue'
import { keysAPI } from '@/api/keys'
import { useAuthStore } from '@/stores/auth'
import type { ApiKey } from '@/types'

const loaded = ref(false)
const loading = ref(false)
const hasAllowedBatchImageKey = ref(false)
const cacheUserId = ref<number | null>(null)
let requestId = 0
let pendingLoad: Promise<boolean> | null = null
const pageSize = 100

function keyAllowsBatchImage(key: ApiKey): boolean {
  return (
    key.status === 'active' &&
    key.group?.platform === 'gemini' &&
    key.group?.allow_batch_image_generation === true
  )
}

async function loadBatchImageAccess(force = false): Promise<boolean> {
  const authStore = useAuthStore()
  const userId = authStore.isAuthenticated ? authStore.user?.id ?? null : null
  if (cacheUserId.value !== userId) {
    cacheUserId.value = userId
    loaded.value = false
    loading.value = false
    hasAllowedBatchImageKey.value = false
    pendingLoad = null
    requestId += 1
  }
  if (userId === null) {
    loaded.value = true
    hasAllowedBatchImageKey.value = false
    return false
  }

  if (loaded.value && !force) {
    return hasAllowedBatchImageKey.value
  }

  if (pendingLoad && !force) {
    return pendingLoad
  }

  const loadId = ++requestId
  const isCurrentLoad = () => loadId === requestId && authStore.isAuthenticated && authStore.user?.id === userId
  loading.value = true
  pendingLoad = (async () => {
    let page = 1
    while (true) {
      const response = await keysAPI.list(page, pageSize, {
        status: 'active',
        sort_by: 'created_at',
        sort_order: 'desc'
      })

      if (!isCurrentLoad()) return false

      if ((response.items || []).some(keyAllowsBatchImage)) {
        hasAllowedBatchImageKey.value = true
        loaded.value = true
        return true
      }

      if (page >= response.pages || (response.items || []).length === 0) {
        hasAllowedBatchImageKey.value = false
        loaded.value = true
        return false
      }

      page += 1
    }
  })()
    .catch(() => {
      if (!isCurrentLoad()) return false
      hasAllowedBatchImageKey.value = false
      loaded.value = true
      return false
    })
    .finally(() => {
      if (loadId !== requestId) return
      loading.value = false
      pendingLoad = null
    })

  return pendingLoad
}

export function useBatchImageAccess() {
  const authStore = useAuthStore()
  const isCurrentUser = computed(() => (authStore.isAuthenticated ? authStore.user?.id ?? null : null) === cacheUserId.value)
  const canUseBatchImage = computed(() => authStore.isAuthenticated && isCurrentUser.value && hasAllowedBatchImageKey.value)

  return {
    canUseBatchImage,
    batchImageAccessLoaded: computed(() => isCurrentUser.value && loaded.value),
    batchImageAccessLoading: computed(() => isCurrentUser.value && loading.value),
    refreshBatchImageAccess: loadBatchImageAccess,
  }
}
