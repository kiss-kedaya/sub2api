interface ItemsPage<T> {
  items: T[]
  total: number
  page_size?: number
}

// Follow the server's actual page size, including a newly lowered table cap.
export async function fetchPaginatedItems<T>(
  fetchPage: (page: number, pageSize: number) => Promise<ItemsPage<T>>,
  requestedPageSize: number,
  maxItems = Infinity
): Promise<T[]> {
  const first = await fetchPage(1, requestedPageSize)
  const target = Math.min(first.total, maxItems)
  const items = first.items.slice(0, target)
  const pageSize = first.page_size || (
    first.items.length > 0 && first.items.length < requestedPageSize
      ? first.items.length
      : requestedPageSize
  )
  for (let page = 2; page <= Math.ceil(target / pageSize) && items.length < target; page++) {
    const result = await fetchPage(page, pageSize)
    if (result.items.length === 0 || (result.page_size && result.page_size !== pageSize)) {
      throw new Error('Incomplete paginated response')
    }
    items.push(...result.items.slice(0, target - items.length))
  }
  if (items.length < target) throw new Error('Incomplete paginated response')
  return items
}
