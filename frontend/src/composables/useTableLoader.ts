import { ref, reactive, onUnmounted } from 'vue'
import { useDebounceFn } from '@vueuse/core'

interface PaginationState {
  page: number
  page_size: number
  total: number
  pages: number
}

interface TableLoaderOptions<T, P> {
  fetchFn: (page: number, pageSize: number, params: P, options?: { signal: AbortSignal }) => Promise<{
    items: T[]
    total: number
    pages: number
  }>
  initialParams?: P
  pageSize?: number
  debounceMs?: number
}

export function useTableLoader<T, P extends Record<string, any>>(options: TableLoaderOptions<T, P>) {
  const { fetchFn, initialParams, pageSize = 20, debounceMs = 300 } = options

  const items = ref<T[]>([])
  const loading = ref(false)
  const params = reactive<P>({ ...(initialParams || {}) } as P)
  const pagination = reactive<PaginationState>({
    page: 1,
    page_size: pageSize,
    total: 0,
    pages: 0
  })

  let abortController: AbortController | null = null

  const isAbortError = (error: any) => {
    return error?.name === 'AbortError' || error?.code === 'ERR_CANCELED'
  }

  const load = async () => {
    if (abortController) {
      abortController.abort()
    }
    abortController = new AbortController()
    loading.value = true

    try {
      const response = await fetchFn(
        pagination.page,
        pagination.page_size,
        params,
        { signal: abortController.signal }
      )
      
      items.value = response.items
      pagination.total = response.total
      pagination.pages = response.pages
    } catch (error) {
      if (!isAbortError(error)) {
        throw error
      }
    } finally {
      if (abortController?.signal.aborted === false) {
        loading.value = false
      }
    }
  }

  const reload = () => {
    pagination.page = 1
    return load()
  }

  const debouncedLoad = useDebounceFn(reload, debounceMs)

  const handlePageChange = (page: number) => {
    pagination.page = page
    load()
  }

  const handlePageSizeChange = (size: number) => {
    pagination.page_size = size
    reload()
  }

  onUnmounted(() => {
    abortController?.abort()
  })

  return {
    items,
    loading,
    params,
    pagination,
    load,
    reload,
    debouncedLoad,
    handlePageChange,
    handlePageSizeChange
  }
}
